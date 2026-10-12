package site

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// A queued row must not deadlock batchStatus. Production runs site.db with
// SetMaxOpenConns(1); a nested query issued while the outer rows are still
// open waits forever for the only connection and freezes the whole site.
// Regression test for the 2026-10-12 production freeze.
func TestBatchStatusQueuedRowNoDeadlock(t *testing.T) {
	dir := t.TempDir()
	db, e := sql.Open("sqlite3", filepath.Join(dir, "site.db")+"?_busy_timeout=5000&_journal_mode=WAL")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1) // same as production
	for _, q := range []string{
		`CREATE TABLE codes (id TEXT PRIMARY KEY, hint TEXT NOT NULL, months INTEGER NOT NULL, status TEXT NOT NULL, message TEXT NOT NULL DEFAULT '', progress INTEGER NOT NULL DEFAULT 0);`,
		`CREATE TABLE redeem_queue (seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE, token TEXT NOT NULL, code_id TEXT NOT NULL, username TEXT NOT NULL, recipient TEXT NOT NULL DEFAULT '', resuming INTEGER NOT NULL DEFAULT 0, state TEXT NOT NULL, message TEXT NOT NULL DEFAULT '', created INTEGER NOT NULL);`,
	} {
		if _, e = db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	// Token A holds seq 1 and 3; another batch's queued row sits at seq 2.
	// The queued items for A must report ahead=0 and ahead=2 respectively.
	seed := []struct{ token, code, user string }{
		{"batchA123456", "c1", "alice"},
		{"batchB123456", "c2", "bob"},
		{"batchA123456", "c3", "carol"},
	}
	now := time.Now().Unix()
	for i, s := range seed {
		if _, e = db.Exec(`INSERT INTO codes(id,hint,months,status,progress) VALUES(?,?,3,'active',0)`, s.code, fmt.Sprintf("hint%d", i)); e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(`INSERT INTO redeem_queue(id,token,code_id,username,state,created) VALUES(?,?,?,?,'queued',?)`, fmt.Sprintf("entry%d", i), s.token, s.code, s.user, now); e != nil {
			t.Fatal(e)
		}
	}
	srv := &server{db: db}
	req := httptest.NewRequest("GET", "/api/batch/batchA123456", nil)
	req.SetPathValue("token", "batchA123456")
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		srv.batchStatus(rec, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("batchStatus did not return within 3s; the single-connection deadlock is back")
	}
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Items []struct {
			Ahead  int    `json:"ahead"`
			Status string `json:"status"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if e = json.Unmarshal(rec.Body.Bytes(), &out); e != nil {
		t.Fatal(e)
	}
	if out.Total != 2 || len(out.Items) != 2 {
		t.Fatalf("want 2 items for token A, got total=%d items=%d", out.Total, len(out.Items))
	}
	if out.Items[0].Ahead != 0 || out.Items[1].Ahead != 2 {
		t.Fatalf("ahead mismatch: %+v", out.Items)
	}
	for _, it := range out.Items {
		if it.Status != "queued" {
			t.Fatalf("item status = %q, want queued", it.Status)
		}
	}
}
