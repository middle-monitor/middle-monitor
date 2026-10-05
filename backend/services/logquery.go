package services

// Datadog-style log search. The grammar and the rewrite rules live in
// searchquery.go and are shared with the trace search.

// logQueryFieldAliases maps Datadog-style facet names to log index fields.
var logQueryFieldAliases = map[string]string{
	"service":  "service_name",
	"status":   "severity_text",
	"severity": "severity_text",
	"host":     "hostname",
}

// Free text also probes trace_id and span_id: pasting an id read off a trace or
// a log detail is the fastest way in, and it used to match nothing.
var logQuerySpec = newQuerySpec(
	logQueryFieldAliases,
	[]string{"body", "service_name", "severity_text", "hostname", "trace_id", "span_id"},
	[]string{"body", "service_name", "severity_text", "hostname", "trace_id", "span_id", "attributes.*"},
)

// ResolveLogFieldAlias maps a Datadog-style facet name to its index field,
// returning the input unchanged when it is not an alias.
func ResolveLogFieldAlias(field string) string {
	return resolveAlias(logQueryFieldAliases, field)
}

// NormalizeLogQuery rewrites Datadog facet aliases outside quoted strings.
func NormalizeLogQuery(q string) string {
	return logQuerySpec.normalize(q)
}

// BuildLogQueryClause returns the OpenSearch clause for a Datadog-style query,
// or nil when the query is empty.
func BuildLogQueryClause(q string) map[string]interface{} {
	return logQuerySpec.clause(q)
}
