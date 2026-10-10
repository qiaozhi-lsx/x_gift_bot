package site

// Batch-session cookie: issued when the user clears Turnstile at the
// "verify codes" step (code-info-batch) and required by the batch endpoints
// (/api/check-batch, /api/redeem-batch) that would otherwise be reachable
// without any human-verification tax at all.
//
// Design choices:
//   - Stateless HMAC token: a 32-byte secret regenerated on each server
//     start signs <expires-at> and the browser carries it as an HttpOnly
//     Lax cookie. Server restart invalidates old tokens; users simply click
//     "验证卡密" again — the UX cost is the same as a session that aged out.
//   - SameSite=Lax + Origin check (upstream middleware) jointly block CSRF
//     from third-party pages; attackers cannot ride a victim's cookie into
//     check-batch / redeem-batch.
//   - 15-minute TTL covers a single operator session (paste code, verify,
//     fill 1-5 usernames, submit) comfortably but caps replay to the same
//     window.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	batchSessionCookie = "xgift-batch"
	batchSessionTTL    = 15 * time.Minute
)

var batchSessionSecret = mustSecret(32)

func mustSecret(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("batch session secret: %v", err)
	}
	return b
}

func (s *server) issueBatchSession(w http.ResponseWriter) {
	expires := time.Now().Add(batchSessionTTL).Unix()
	payload := strconv.FormatInt(expires, 10)
	mac := hmac.New(sha256.New, batchSessionSecret)
	mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	http.SetCookie(w, &http.Cookie{
		Name:     batchSessionCookie,
		Value:    payload + "." + sig,
		Path:     "/",
		MaxAge:   int(batchSessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func verifyBatchSession(r *http.Request) bool {
	c, err := r.Cookie(batchSessionCookie)
	if err != nil {
		return false
	}
	parts := strings.SplitN(c.Value, ".", 2)
	if len(parts) != 2 {
		return false
	}
	expires, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || expires < time.Now().Unix() {
		return false
	}
	mac := hmac.New(sha256.New, batchSessionSecret)
	mac.Write([]byte(parts[0]))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(parts[1]), []byte(expected))
}

// requireBatchSession gates the batch endpoints behind one Turnstile pass.
// A bot can still pay the Turnstile cost once per IP but each batch endpoint
// then stays rate-limited (redeem 4/min, check 8/min) under the same cookie.
func (s *server) requireBatchSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !verifyBatchSession(r) {
			message(w, 403, "请先完成卡密验证再继续。")
			return
		}
		next(w, r)
	}
}
