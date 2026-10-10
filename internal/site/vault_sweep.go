package site

// Vault sweeper: delete long-tail records that otherwise grow unbounded and
// slowly degrade aggregateCardUsage / recovery scanning.
//
// Scope of each prefix:
//   admin-recovery:<id>       — recovery previews/runs. "latest" is special.
//   admin-recovery-error:<id> — error payloads
//   redemption-failure:<id>:<nano> — per-order failure logs
//   manual-recovery-error:<id>    — manual recovery error logs
//   public-checkout-history:<id>:<nano> — pre-replacement snapshots
//   stripe-route-history:<id>:<ts>     — route rotation history
//   checkout-retired:<id>:<session>    — archived live checkouts
//
// Policy: delete entries older than their respective TTL. "latest" / "active"
// aliases are never touched. Each sweep decrypts only the entries it is
// about to prune (via timestamp embedded in the key where possible, else
// via Get+parse). We prefer parsing timestamps out of the key to skip
// decrypting good records.

import (
	"log"
	"strconv"
	"strings"
	"time"
)

const (
	vaultSweepPeriod = 6 * time.Hour

	ttlAdminRecoveryError   = 30 * 24 * time.Hour
	ttlRedemptionFailure    = 60 * 24 * time.Hour
	ttlManualRecoveryError  = 30 * 24 * time.Hour
	ttlPublicCheckoutHistory = 30 * 24 * time.Hour
	ttlStripeRouteHistory   = 14 * 24 * time.Hour
	ttlCheckoutRetired      = 180 * 24 * time.Hour
)

func (s *server) vaultSweepLoop() {
	ticker := time.NewTicker(vaultSweepPeriod)
	defer ticker.Stop()
	// 启动后 2 分钟先跑一轮,别让服务一上来就卡在空闲 6 小时才清。
	first := time.NewTimer(2 * time.Minute)
	for {
		select {
		case <-s.ctx.Done():
			first.Stop()
			return
		case <-first.C:
			s.sweepVault()
		case <-ticker.C:
			s.sweepVault()
		}
	}
}

func (s *server) sweepVault() {
	now := time.Now()
	type rule struct {
		prefix string
		ttl    time.Duration
		keep   func(name string) bool // 返回 true 表示保留(例如 "latest")
	}
	rules := []rule{
		{"admin-recovery-error:", ttlAdminRecoveryError, nil},
		{"redemption-failure:", ttlRedemptionFailure, nil},
		{"manual-recovery-error:", ttlManualRecoveryError, nil},
		{"public-checkout-history:", ttlPublicCheckoutHistory, nil},
		{"stripe-route-history:", ttlStripeRouteHistory, nil},
		{"checkout-retired:", ttlCheckoutRetired, nil},
		{"admin-recovery:", 7 * 24 * time.Hour, func(n string) bool {
			return strings.HasSuffix(n, ":latest")
		}},
	}
	for _, rl := range rules {
		s.sweepVaultPrefix(rl.prefix, rl.ttl, now, rl.keep)
	}
}

// sweepVaultPrefix walks records under prefix, deleting any whose embedded
// ":<nano>" or ":<ts>" suffix is older than ttl. Entries without a parseable
// timestamp are left intact so a schema change never nukes legitimate data.
func (s *server) sweepVaultPrefix(prefix string, ttl time.Duration, now time.Time, keep func(string) bool) {
	names, err := s.vault.NamesWithPrefix(prefix)
	if err != nil {
		log.Printf("vault sweep (%s) list failed", prefix)
		return
	}
	cutoff := now.Add(-ttl)
	deleted := 0
	for _, name := range names {
		if keep != nil && keep(name) {
			continue
		}
		ts, ok := extractTimestamp(name[len(prefix):])
		if !ok {
			continue
		}
		if ts.After(cutoff) {
			continue
		}
		if err := s.vault.Delete(name); err != nil {
			continue
		}
		deleted++
	}
	if deleted > 0 {
		log.Printf("vault sweep (%s): deleted %d record(s) older than %s", prefix, deleted, ttl)
	}
}

// extractTimestamp parses a trailing numeric suffix as either Unix seconds
// (10 digits) or Unix nanoseconds (19 digits). Also accepts the Go-style
// "20060102T150405.000000000" layout used in stripe-route-history keys.
func extractTimestamp(remainder string) (time.Time, bool) {
	// Find the last ':' and look at what follows.
	if i := strings.LastIndex(remainder, ":"); i >= 0 {
		tail := remainder[i+1:]
		if ts, ok := parseStringTs(tail); ok {
			return ts, true
		}
	}
	// Some records only have a bare suffix (no colon).
	return parseStringTs(remainder)
}

func parseStringTs(s string) (time.Time, bool) {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		switch {
		case n >= 1_000_000_000_000_000_000: // nanoseconds
			return time.Unix(0, n), true
		case n >= 1_000_000_000: // seconds
			return time.Unix(n, 0), true
		}
	}
	// Go stripe-route-history layout "20060102T150405.000000000".
	if t, err := time.Parse("20060102T150405.000000000", s); err == nil {
		return t, true
	}
	return time.Time{}, false
}
