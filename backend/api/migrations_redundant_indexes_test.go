package api

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// service_results takes every check result of every service: it is the most
// written table of the product, and each index on it is paid again on every
// insert. An index whose columns are the prefix of another one is never chosen
// by the planner over the longer one, so it buys nothing and only costs that
// write — idx_service_results_service_id against
// idx_service_results_service_id_timestamp is exactly that case. The waste is
// invisible at read time, which is why it needs a test rather than a review.

var (
	createIndexStmtRE = regexp.MustCompile(`(?is)CREATE\s+INDEX\s+(?:IF\s+NOT\s+EXISTS\s+)?(\w+)\s+ON\s+(\w+)\s*\(([^)]*)\)`)
	dropIndexStmtRE   = regexp.MustCompile(`(?is)DROP\s+INDEX\s+(?:IF\s+EXISTS\s+)?(\w+)`)
)

// effectiveIndexes returns the indexes left on table once every up migration
// has run, as index name to column list normalized to lowercase single-spaced
// text.
func effectiveIndexes(t *testing.T, table string) map[string]string {
	t.Helper()

	// Glob sorts its results, and the NNNNNN_ prefix is zero padded: migrations
	// are read in the order they are applied, so a later DROP wins.
	entries, err := fs.Glob(migrationsFS, "migrations/*.up.sql")
	if err != nil || len(entries) == 0 {
		t.Fatalf("no up migrations embedded: %v", err)
	}

	indexes := map[string]string{}
	for _, entry := range entries {
		content, err := fs.ReadFile(migrationsFS, entry)
		if err != nil {
			t.Fatalf("reading %s: %v", entry, err)
		}
		statements := string(content)
		for _, m := range createIndexStmtRE.FindAllStringSubmatch(statements, -1) {
			if !strings.EqualFold(m[2], table) {
				continue
			}
			indexes[strings.ToLower(m[1])] = strings.Join(strings.Fields(strings.ToLower(strings.ReplaceAll(m[3], `"`, ""))), " ")
		}
		for _, m := range dropIndexStmtRE.FindAllStringSubmatch(statements, -1) {
			delete(indexes, strings.ToLower(m[1]))
		}
	}
	return indexes
}

func TestMigrationsLeaveNoServiceIDOnlyIndexOnServiceResults(t *testing.T) {
	for name, cols := range effectiveIndexes(t, "service_results") {
		if cols == "service_id" {
			t.Fatalf("%s indexes service_results(service_id) alone; the leading column of (service_id, timestamp DESC) already serves those lookups, so the write cost on the most written table of the product is paid for nothing", name)
		}
	}
}

func TestMigrationsLeaveNoIndexCoveredByAnotherOnServiceResults(t *testing.T) {
	indexes := effectiveIndexes(t, "service_results")

	for name, cols := range indexes {
		for otherName, otherCols := range indexes {
			if name == otherName || cols == otherCols {
				continue
			}
			if strings.HasPrefix(otherCols, cols+", ") {
				t.Errorf("%s(%s) is a prefix of %s(%s): no plan can prefer the shorter index, only its write cost is real", name, cols, otherName, otherCols)
			}
		}
	}
}
