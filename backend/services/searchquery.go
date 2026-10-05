package services

import (
	"regexp"
	"sort"
	"strings"
)

// Datadog-style search, shared by the log and the trace views.
//
// The raw query is handed to an OpenSearch query_string clause, whose Lucene
// syntax natively covers everything the UI advertises: implicit AND between
// terms, AND/&&, OR/||, NOT/- exclusion, parentheses, quoted phrases,
// field:value facets and * wildcards. Each signal supplies its own facet
// aliases and the fields free text is matched against.

type querySpec struct {
	aliases map[string]string
	aliasRe *regexp.Regexp
	spaceRe *regexp.Regexp
	fields  []string
}

// newQuerySpec compiles the rewrite rules for one signal. indexFields lists the
// names a user may type before a colon on top of the aliases; only those are
// space-corrected, so free text like "timeout: refused" keeps its colon.
func newQuerySpec(aliases map[string]string, indexFields, fields []string) *querySpec {
	aliasNames := make([]string, 0, len(aliases))
	for name := range aliases {
		aliasNames = append(aliasNames, name)
	}
	sort.Strings(aliasNames)

	facets := append(append([]string{}, aliasNames...), indexFields...)
	sort.Strings(facets)

	return &querySpec{
		aliases: aliases,
		aliasRe: regexp.MustCompile(`\b(` + strings.Join(aliasNames, "|") + `):`),
		spaceRe: regexp.MustCompile(`\b(` + strings.Join(facets, "|") + `):[ \t]+`),
		fields:  fields,
	}
}

// normalize rewrites facet aliases and closes the space left after a colon:
// "service: api" reaches Lucene as an empty field term and matches nothing,
// which reads as a broken search. Quoted phrases are user text, left verbatim.
func (s *querySpec) normalize(q string) string {
	segments := strings.Split(q, `"`)
	for i := 0; i < len(segments); i += 2 { // even indexes are outside quotes
		segments[i] = s.spaceRe.ReplaceAllString(segments[i], "$1:")
		segments[i] = s.aliasRe.ReplaceAllStringFunc(segments[i], func(m string) string {
			return s.aliases[strings.TrimSuffix(m, ":")] + ":"
		})
	}
	return strings.Join(segments, `"`)
}

// clause returns the OpenSearch clause for a query, or nil when it is empty.
// lenient tolerates type mismatches (a bare term probed against a date field);
// a syntactically invalid query makes OpenSearch answer 400, which
// executeSearch surfaces as zero results.
func (s *querySpec) clause(q string) map[string]interface{} {
	if strings.TrimSpace(q) == "" {
		return nil
	}
	return map[string]interface{}{
		"query_string": map[string]interface{}{
			"query":            s.normalize(q),
			"fields":           s.fields,
			"default_operator": "AND",
			"lenient":          true,
			"analyze_wildcard": true,
		},
	}
}

func resolveAlias(aliases map[string]string, field string) string {
	if resolved, ok := aliases[field]; ok {
		return resolved
	}
	return field
}
