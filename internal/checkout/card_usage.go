package checkout

import (
	"encoding/json"
	"log"
	"math"
	"sync"
	"time"
	"xgift/internal/vault"
)

// PlanCostUSD 声明每种套餐"卡实际扣款的 USD 等值"。Stripe 账单货币是 BDT
// (catalog.currency),Record.Amount 存的是 BDT minor units,但到卡上收到的
// charge 是 USD。**运维必须保证这个 map 跟 Stripe 后台公布的 USD 等值一致**;
// 一旦 Stripe 调价(促销/汇率/A-B 测试),这里也要跟着改,否则 CardStatus 的
// Remaining 和 Depleted 判定会跟真实卡余额漂移。
//
// 下一笔付款从 catalog 读 months 后在这里查 USD 代价。没在表里的 months 不是
// 没花钱 —— 它只是没定价,aggregateCardUsage 会用 UnpricedMonths 单独报
// 告给运维,**不**静默扔掉。
var PlanCostUSD = map[int]float64{
	3: 2.45,
	6: 4.95,
}

// PlanLabel returns the short Chinese label used in admin UIs.
// Kept for CLI/log convenience; the production plan set is 3→季卡, 6→半年卡.
func PlanLabel(months int) string {
	switch months {
	case 3:
		return "季卡"
	case 6:
		return "半年卡"
	default:
		return ""
	}
}

// CardUsage summarizes historical charges tied to one card fingerprint,
// derived from live and retired checkout records.
type CardUsage struct {
	Count     int         `json:"count"`
	USD       float64     `json:"usd"`
	PerMonths map[int]int `json:"per_months,omitempty"` // {3: 2, 6: 1}
	InFlight  int         `json:"in_flight,omitempty"`  // in-flight 已 pin 卡但未落账的订单计数
}

// usageCache keeps a short-lived in-process aggregation to spare the hot
// payment-selection path from re-scanning + re-decrypting every checkout
// record. 10 s is small enough that an operator-visible stat lag is in the
// same order as reconcile cadence, but large enough to cover a burst of
// route selections within one batch submission.
type cachedUsage struct {
	data map[string]*CardUsage
	at   time.Time
}

var (
	usageCacheMu  sync.RWMutex
	usageCacheRef *cachedUsage
)

const usageCacheTTL = 10 * time.Second

// invalidateUsageCache drops the cached aggregation so the next read sees
// fresh data. Must be called whenever a checkout record state transitions
// (success landing, retire, decline) that would change per-card totals.
func invalidateUsageCache() {
	usageCacheMu.Lock()
	usageCacheRef = nil
	usageCacheMu.Unlock()
}

// countsForLimit reports whether a Record counts against a card's remaining
// Limit. Succeeded orders are the ground truth; in-flight orders (submitted
// / requires_action / unknown but with a bound Stripe session and a positive
// Amount) must count too, otherwise concurrent requests can pin the same
// card after one order's authorization has been issued but before its
// `succeeded` state lands, and both end up pulling budget the card never
// had. "declined" / "canceled" / "" / "revoked" do not count.
func countsForLimit(r Record) bool {
	if r.CardFingerprint == "" || r.Amount <= 0 {
		return false
	}
	switch r.Status {
	case "succeeded":
		return true
	case "submitted", "requires_action", "unknown":
		// 已经把 payment_method 塞给 Stripe、或者等用户 3DS、或者 confirm
		// 返回后状态未明——这些订单的 authorization 要么已占用,要么马上会。
		// 要求 SubmittedAt > 0 以排除刚建 Record 但还没触碰 Stripe 的空壳。
		return r.SubmittedAt > 0
	}
	return false
}

// aggregateCardUsage scans both live and retired checkout records and groups
// relevant charges by card fingerprint. SessionID deduplicates a record that
// exists in both the live and retired prefixes for the same recipient.
//
// Cost source: PlanCostUSD[months] — the operator-declared USD charge a card
// actually sees. Record.Amount is Stripe's billing-currency amount (BDT), not
// what hits the card, so we never use it for Limit math. Months absent from
// PlanCostUSD get a one-line warn log and are left out of USD/Count but still
// bump an "unpriced" counter so operators can audit the gap.
//
// Succeeded rows feed USD/Count/PerMonths; in-flight rows (submitted /
// requires_action / unknown with SubmittedAt > 0) additionally bump InFlight
// so a card pinned to a running order cannot be re-pinned past its Limit
// before the first order's succeeded state lands.
func aggregateCardUsage(v *vault.Vault) (map[string]*CardUsage, error) {
	// Fast path: fresh cached aggregation.
	usageCacheMu.RLock()
	if usageCacheRef != nil && time.Since(usageCacheRef.at) < usageCacheTTL {
		cached := usageCacheRef.data
		usageCacheMu.RUnlock()
		return cloneUsageMap(cached), nil
	}
	usageCacheMu.RUnlock()

	out := map[string]*CardUsage{}
	seen := map[string]struct{}{} // SessionID
	unpriced := map[int]int{}     // months → count,用于最后一次性 warn
	for _, prefix := range []string{"checkout:", "checkout-retired:"} {
		names, err := v.NamesWithPrefix(prefix)
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			raw, err := v.Get(name)
			if err != nil {
				continue
			}
			var r Record
			if json.Unmarshal(raw, &r) != nil {
				clear(raw)
				continue
			}
			clear(raw)
			if !countsForLimit(r) || r.SessionID == "" {
				continue
			}
			if _, dup := seen[r.SessionID]; dup {
				continue
			}
			seen[r.SessionID] = struct{}{}
			cost, priced := PlanCostUSD[r.Months]
			if !priced {
				unpriced[r.Months]++
				// 月份没定价不代表没花钱,而是运维还没把这个套餐的 USD 等值
				// 填进 PlanCostUSD。不计入 USD 以免估错,但别把订单也丢掉,
				// 让下面 InFlight / Count 仍能反映真实活动。
			}
			u := out[r.CardFingerprint]
			if u == nil {
				u = &CardUsage{PerMonths: map[int]int{}}
				out[r.CardFingerprint] = u
			}
			if r.Status == "succeeded" {
				u.Count++
				u.PerMonths[r.Months]++
				if priced {
					u.USD = roundCents(u.USD + cost)
				}
			} else {
				u.InFlight++
				if priced {
					u.USD = roundCents(u.USD + cost)
				}
			}
		}
	}
	for months, n := range unpriced {
		log.Printf("aggregateCardUsage: %d charges with months=%d have no entry in PlanCostUSD; UsedUSD is understated for every card that ran them", n, months)
	}

	usageCacheMu.Lock()
	usageCacheRef = &cachedUsage{data: out, at: time.Now()}
	usageCacheMu.Unlock()
	return cloneUsageMap(out), nil
}

func cloneUsageMap(src map[string]*CardUsage) map[string]*CardUsage {
	out := make(map[string]*CardUsage, len(src))
	for k, v := range src {
		clone := &CardUsage{Count: v.Count, USD: v.USD, InFlight: v.InFlight}
		if len(v.PerMonths) > 0 {
			clone.PerMonths = make(map[int]int, len(v.PerMonths))
			for m, n := range v.PerMonths {
				clone.PerMonths[m] = n
			}
		}
		out[k] = clone
	}
	return out
}

func roundCents(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*100) / 100
}

// nextCost reports the USD cost of the cheapest plan the catalog would next
// serve. Resolved against PlanCostUSD (not catalog.Amount, which is in
// Stripe's billing currency / BDT). A plan configured in catalog but missing
// from PlanCostUSD is skipped — operators get a warn log so they know to add
// it. Fallback 2.45 covers an empty catalog so Depleted doesn't match $0.
func nextCost(cat Catalog) float64 {
	min := 0.0
	for _, p := range cat.Plans {
		cost, ok := PlanCostUSD[p.Months]
		if !ok {
			log.Printf("nextCost: catalog plan months=%d missing from PlanCostUSD; it will not be used for Limit math", p.Months)
			continue
		}
		if min == 0 || cost < min {
			min = cost
		}
	}
	if min <= 0 {
		min = 2.45
	}
	return min
}

// PlanCostsUSD is the public read-only snapshot of PlanCostUSD for the admin
// frontend. Returning a plain float map keeps the client contract explicit:
// "these are the USD amounts you can show as 成本".
func PlanCostsUSD() map[int]float64 {
	out := make(map[int]float64, len(PlanCostUSD))
	for k, v := range PlanCostUSD {
		out[k] = v
	}
	return out
}
