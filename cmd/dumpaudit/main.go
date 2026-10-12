package main

import (
	"flag"
	"fmt"
	"os"

	"xgift/internal/vault"
)

// dumpaudit reads encrypted vault records by name prefix and prints them as
// plain text. Diagnostic tool for inspecting stored X audit envelopes.
func main() {
	db := flag.String("db", "", "vault db path")
	pw := flag.String("password-file", "", "owner-only password file")
	prefix := flag.String("prefix", "", "record name prefix")
	flag.Parse()
	if *db == "" || *pw == "" || *prefix == "" {
		fmt.Fprintln(os.Stderr, "usage: dumpaudit --db <vault.db> --password-file <pw> --prefix <prefix>")
		os.Exit(1)
	}
	v, err := vault.Open(*db, *pw, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open:", err)
		os.Exit(1)
	}
	defer v.Close()
	names, err := v.NamesWithPrefix(*prefix)
	if err != nil {
		fmt.Fprintln(os.Stderr, "list:", err)
		os.Exit(1)
	}
	fmt.Printf("%d record(s) with prefix %q\n", len(names), *prefix)
	for _, n := range names {
		b, err := v.Get(n)
		if err != nil {
			fmt.Printf("=== %s === ERROR: %v\n", n, err)
			continue
		}
		fmt.Printf("=== %s ===\n%s\n", n, string(b))
	}
}
