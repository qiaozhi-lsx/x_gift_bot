package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"xgift/internal/vault"
)

// archiveone atomically retires one vault record: it wraps the current value
// with a retirement note, archives it under the given prefix plus a timestamp,
// and removes the active key. Contents never appear in normal output.
func main() {
	db := flag.String("db", "", "vault db path")
	pw := flag.String("password-file", "", "owner-only password file")
	name := flag.String("name", "", "exact record name, e.g. checkout:<recipient>")
	prefix := flag.String("archive-prefix", "", "archive name prefix, e.g. checkout-retired:<id>:")
	reason := flag.String("reason", "", "why this record is being retired")
	flag.Parse()
	if *db == "" || *pw == "" || *name == "" || *prefix == "" || *reason == "" {
		fmt.Fprintln(os.Stderr, "usage: archiveone --db <vault.db> --password-file <pw> --name checkout:<id> --archive-prefix checkout-retired:<id>: --reason <text>")
		os.Exit(1)
	}
	v, err := vault.Open(*db, *pw, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open:", err)
		os.Exit(1)
	}
	defer v.Close()
	cur, err := v.Get(*name)
	if err != nil {
		fmt.Fprintln(os.Stderr, "get:", err)
		os.Exit(1)
	}
	defer clear(cur)
	var shape struct {
		Status    string `json:"status"`
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(cur, &shape) != nil {
		fmt.Fprintln(os.Stderr, "record is not a recognizable checkout envelope")
		os.Exit(1)
	}
	now := time.Now()
	proof, err := json.Marshal(struct {
		Record        json.RawMessage `json:"record"`
		RetiredAt     int64           `json:"retired_at"`
		RetiredReason string          `json:"retired_reason"`
	}{cur, now.Unix(), *reason})
	if err != nil {
		fmt.Fprintln(os.Stderr, "marshal:", err)
		os.Exit(1)
	}
	defer clear(proof)
	archive := fmt.Sprintf("%s%d", *prefix, now.UnixNano())
	if err := v.Archive(*name, archive, cur, proof); err != nil {
		fmt.Fprintln(os.Stderr, "archive:", err)
		os.Exit(1)
	}
	fmt.Printf("retired %s (status=%s session=%t) -> %s\n", *name, shape.Status, shape.SessionID != "", archive)
}
