package services

// Datadog-style trace search, same grammar as the log search so a query typed
// in one view behaves the same in the other.

// traceQueryFieldAliases maps Datadog-style facet names to trace index fields.
var traceQueryFieldAliases = map[string]string{
	"service":   "service_name",
	"operation": "operation_name",
	"host":      "hostname",
	"status":    "status_code",
	"kind":      "span_kind",
}

var traceQuerySpec = newQuerySpec(
	traceQueryFieldAliases,
	[]string{"operation_name", "service_name", "span_kind", "status_code", "hostname", "trace_id", "span_id", "parent_span_id", "duration_ms"},
	[]string{"operation_name", "service_name", "span_kind", "status_code", "hostname", "trace_id", "span_id", "parent_span_id", "attributes.*"},
)

// ResolveTraceFieldAlias maps a Datadog-style facet name to its index field,
// returning the input unchanged when it is not an alias.
func ResolveTraceFieldAlias(field string) string {
	return resolveAlias(traceQueryFieldAliases, field)
}

// NormalizeTraceQuery rewrites Datadog facet aliases outside quoted strings.
func NormalizeTraceQuery(q string) string {
	return traceQuerySpec.normalize(q)
}

// BuildTraceQueryClause returns the OpenSearch clause for a Datadog-style
// query, or nil when the query is empty.
func BuildTraceQueryClause(q string) map[string]interface{} {
	return traceQuerySpec.clause(q)
}
