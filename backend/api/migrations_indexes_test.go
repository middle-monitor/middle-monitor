package api

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// Reading the last n results of a service is only fast while every service
// keeps writing: the global timestamp index is walked from the head and the
// service's rows are found right away. On a silent service that same plan reads
// and discards everything written since it stopped — 188 431 rows and 66 ms
// after three days in production — and degrades until retention catches up.
// Only (service_id, timestamp DESC), in that order and that direction, lets the
// scan start at the service's newest row and stop after n. A single-column
// index on either column, or the same pair in the other order, silently brings
// the degradation back, and nothing in nominal conditions shows it.

var createIndexRE = regexp.MustCompile(`(?is)CREATE\s+INDEX\s+(?:IF\s+NOT\s+EXISTS\s+)?\w+\s+ON\s+(\w+)\s*\(([^)]*)\)`)

// indexedColumns returns the column list of every non-partial index the
// migrations create on table, normalized to lowercase single-spaced text.
func indexedColumns(t *testing.T, table string) []string {
	t.Helper()

	entries, err := fs.Glob(migrationsFS, "migrations/*.up.sql")
	if err != nil || len(entries) == 0 {
		t.Fatalf("no up migrations embedded: %v", err)
	}

	var cols []string
	for _, entry := range entries {
		content, err := fs.ReadFile(migrationsFS, entry)
		if err != nil {
			t.Fatalf("reading %s: %v", entry, err)
		}
		for _, m := range createIndexRE.FindAllStringSubmatch(string(content), -1) {
			if !strings.EqualFold(m[1], table) {
				continue
			}
			cols = append(cols, strings.Join(strings.Fields(strings.ToLower(strings.ReplaceAll(m[2], `"`, ""))), " "))
		}
	}
	return cols
}

func TestMigrationsIndexServiceResultsForTheLatestRowsOfOneService(t *testing.T) {
	const want = "service_id, timestamp desc"

	for _, cols := range indexedColumns(t, "service_results") {
		if cols == want {
			return
		}
	}
	t.Fatalf("no migration creates service_results(%s); the last results of a service that stopped emitting are read by scanning the global timestamp index, got %v",
		want, indexedColumns(t, "service_results"))
}

func TestMigrationsIndexServicesByHost(t *testing.T) {
	for _, cols := range indexedColumns(t, "services") {
		if cols == "host_id" {
			return
		}
	}
	t.Fatal("no migration creates services(host_id); resolving the services of a host scans the services table once per host")
}
