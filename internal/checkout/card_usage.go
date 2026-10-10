package checkout

import (
	"encoding/json"
	"math"
	"sync"
	"time"
	"xgift/internal/vault"
)

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
// exists in both the live and retired prefixes for the same recipient. The
// cost per record is derived from Record.Amount(minor units of r.Currency);
// non-USD rows are skipped because the Limit on each card is denominated in
// USD. Succeeded rows feed USD/Count/PerMonths; in-flight rows additionally
// bump InFlight so operators can see what is currently pending.
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
			// Limit 以 USD 为语义;非 USD 记录(目录未来可能开)此处跳过并
			// 保留 metric 入口,这是显式的"未支持"信号,优于默默合并。
			if r.Currency != "" && r.Currency != "usd" {
				continue
			}
			u := out[r.CardFingerprint]
			if u == nil {
				u = &CardUsage{PerMonths: map[int]int{}}
				out[r.CardFingerprint] = u
			}
			cost := float64(r.Amount) / 100.0
			if r.Status == "succeeded" {
				u.Count++
				u.USD = roundCents(u.USD + cost)
				u.PerMonths[r.Months]++
			} else {
				u.InFlight++
				u.USD = roundCents(u.USD + cost)
			}
		}
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

// nextCost reports the USD cost of the cheapest configured plan. A card
// with less than this remaining is useless. Reads from catalog so a
// run-time price change immediately flows into Depleted detection.
// Non-USD catalogs collapse to a sensible 2.45 fallback (3-month plan)
// — a non-USD catalog isn't production today; Important finding 11 covers
// the broader multi-currency gap.
func nextCost(cat Catalog) float64 {
	if cat.Currency != "" && cat.Currency != "usd" {
		return 2.45
	}
	min := 0.0
	for _, p := range cat.Plans {
		cost := float64(p.Amount) / 100.0
		if min == 0 || cost < min {
			min = cost
		}
	}
	if min <= 0 {
		min = 2.45
	}
	return min
}
