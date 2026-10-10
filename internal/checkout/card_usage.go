package checkout

import (
	"encoding/json"
	"strings"
	"xgift/internal/vault"
)

// PlanCostUSD maps subscription months to the USD cost of one Stripe
// authorization for that plan. Values match the live catalog
// (3 月 = 季卡 = $2.45,6 月 = 半年卡 = $4.95).
var PlanCostUSD = map[int]float64{
	3: 2.45,
	6: 4.95,
}

// PlanLabel returns the short Chinese label used in admin UIs.
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
	Count     int            `json:"count"`
	USD       float64        `json:"usd"`
	PerMonths map[int]int    `json:"per_months,omitempty"` // {3: 2, 6: 1}
}

// aggregateCardUsage scans both live and retired checkout records and groups
// succeeded charges by card fingerprint. SessionID deduplicates a record that
// exists in both the live and retired prefixes for the same recipient. The
// cost per record is derived from PlanCostUSD; unknown month values are
// silently skipped (they cannot be priced).
func aggregateCardUsage(v *vault.Vault) (map[string]*CardUsage, error) {
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
			if r.Status != "succeeded" || r.CardFingerprint == "" || r.SessionID == "" {
				continue
			}
			if _, dup := seen[r.SessionID]; dup {
				continue
			}
			seen[r.SessionID] = struct{}{}
			cost, ok := PlanCostUSD[r.Months]
			if !ok {
				continue
			}
			u := out[r.CardFingerprint]
			if u == nil {
				u = &CardUsage{PerMonths: map[int]int{}}
				out[r.CardFingerprint] = u
			}
			u.Count++
			u.USD += cost
			u.PerMonths[r.Months]++
		}
	}
	// Round USD to two decimals so clients never see e.g. $9.850000000000001.
	for _, u := range out {
		u.USD = roundCents(u.USD)
	}
	_ = strings.TrimSpace // keep import tidy if PlanLabel is removed
	return out, nil
}

func roundCents(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}

// nextCost reports the cost of the cheapest configured plan the catalog
// might serve next. A card with less than this remaining is useless.
func nextCost(cat Catalog) float64 {
	min := 0.0
	for _, p := range cat.Plans {
		if cost, ok := PlanCostUSD[p.Months]; ok {
			if min == 0 || cost < min {
				min = cost
			}
		}
	}
	if min == 0 {
		min = 2.45 // sensible default: 3-month plan
	}
	return min
}
