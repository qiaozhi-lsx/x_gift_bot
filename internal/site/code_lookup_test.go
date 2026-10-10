package site

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func lookupFixture(t *testing.T, code, state string) *server {
	t.Helper()
	db, err := openSiteDB(filepath.Join(t.TempDir(), "site.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`INSERT INTO codes(id,hash,hint,batch,months,status,username,message,created,updated,progress)
 VALUES('lookup',?,'TEST','private batch',6,?,'recipient','private provider details',100,200,42)`, hash(code), state)
	if err != nil {
		t.Fatal(err)
	}
	// No vault or worker: a lookup must not reconcile or initiate a payment.
	return &server{db: db}
}

func lookupRequest(s *server, code string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"code": code})
	r := httptest.NewRequest(http.MethodPost, "/api/code-lookup", strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.codeLookup(w, r)
	return w
}

func TestCodeLookupPersistedStates(t *testing.T) {
	for _, code := range []string{"XPP-1234567890ABCDEF", "XG-" + strings.Repeat("A", 48)} {
		for _, state := range []string{"active", "processing", "succeeded", "review", "revoked"} {
			t.Run(code[:3]+state, func(t *testing.T) {
				s := lookupFixture(t, code, state)
				w := lookupRequest(s, "  "+strings.ToLower(code)+"  ")
				var result map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if w.Code != 200 || result["status"] != state || result["username_masked"] != "@re***t" || result["months"] != float64(6) {
					t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
				}
				if state == "succeeded" && result["progress"] != float64(100) {
					t.Fatal("a redeemed code should report complete progress")
				}
				for _, private := range []string{"recipient", "private provider", "private batch", code, `"id"`, `"username"`} {
					if strings.Contains(w.Body.String(), private) {
						t.Fatalf("lookup exposed %q", private)
					}
				}
				c, err := s.find(code)
				if err != nil || c.Status != state || c.Progress != 42 || c.Updated != 200 {
					t.Fatalf("read-only lookup changed code: %+v %v", c, err)
				}
			})
		}
	}
}

func TestCodeLookupQueueAndErrors(t *testing.T) {
	code := "XPP-1234567890ABCDEF"
	s := lookupFixture(t, code, "active")
	for _, row := range []string{
		`INSERT INTO redeem_queue(id,token,code_id,username,state,created) VALUES('prior','secret-token','other','other','running',100)`,
		`INSERT INTO redeem_queue(id,token,code_id,username,state,created) VALUES('current','secret-token','lookup','queued_user','queued',100)`,
	} {
		if _, err := s.db.Exec(row); err != nil {
			t.Fatal(err)
		}
	}
	w := lookupRequest(s, code)
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || result["status"] != "processing" || result["queued"] != true || result["ahead"] != float64(1) || result["username_masked"] != "@qu***r" {
		t.Fatalf("queued status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "secret-token") {
		t.Fatal("lookup leaked batch token")
	}
	if _, err := s.db.Exec("UPDATE redeem_queue SET state='running' WHERE id='current'"); err != nil {
		t.Fatal(err)
	}
	w = lookupRequest(s, code)
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result["queued"] != false || result["status"] != "processing" {
		t.Fatalf("running body=%s err=%v", w.Body.String(), err)
	}
	// A stale queue row must never hide an authoritative terminal code state.
	for _, terminal := range []string{"succeeded", "revoked"} {
		if _, err := s.db.Exec("UPDATE codes SET status=? WHERE id='lookup'", terminal); err != nil {
			t.Fatal(err)
		}
		w = lookupRequest(s, code)
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result["status"] != terminal || result["queued"] != false {
			t.Fatalf("terminal state overwritten: body=%s err=%v", w.Body.String(), err)
		}
	}
	if w = lookupRequest(s, "bad"); w.Code != 400 {
		t.Fatalf("invalid format returned %d", w.Code)
	}
	if w = lookupRequest(s, "XPP-0000000000000000"); w.Code != 404 || !strings.Contains(w.Body.String(), "not_found") {
		t.Fatalf("unknown code status=%d body=%s", w.Code, w.Body.String())
	}
	s.db.Close()
	if w = lookupRequest(s, code); w.Code != 503 || !strings.Contains(w.Body.String(), "service_unavailable") {
		t.Fatalf("database outage classified incorrectly: %d %s", w.Code, w.Body.String())
	}
}

func TestCodeLookupRequiresHumanWhenConfigured(t *testing.T) {
	s := &server{turnstileSiteKey: "configured"}
	r := httptest.NewRequest(http.MethodPost, "/api/code-lookup", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	s.human("code-info", s.codeLookup)(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("missing human token returned %d", w.Code)
	}
}

func TestCodeInfoBatchDistinguishesUnavailableFromMissing(t *testing.T) {
	s := lookupFixture(t, "XPP-1234567890ABCDEF", "active")
	request := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/code-info-batch", strings.NewReader(`{"codes":["XPP-0000000000000000"]}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.codeInfoBatch(w, r)
		return w
	}
	if w := request(); w.Code != 200 || !strings.Contains(w.Body.String(), `"error":"not_found"`) || strings.Contains(w.Body.String(), `"retry":true`) {
		t.Fatalf("missing code body=%s", w.Body.String())
	}
	s.db.Close()
	if w := request(); w.Code != 200 || !strings.Contains(w.Body.String(), `"error":"service_unavailable"`) || !strings.Contains(w.Body.String(), `"retry":true`) {
		t.Fatalf("transient error body=%s", w.Body.String())
	}
}
