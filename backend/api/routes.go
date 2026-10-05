package api

import (
	"regexp"
	"sort"
	"strings"
)

// RegisteredRoute is one path the router serves, as declared in server.go, with
// the handler behind each method.
type RegisteredRoute struct {
	Path     string
	Methods  []string
	Handlers map[string]string
}

// routeLine matches a route registration in server.go, capturing the subrouter
// it is declared on (which says whether the path is org-scoped), the path, the
// handler, and the methods.
var routeLine = regexp.MustCompile(
	`(?m)^\s*(\w+)\.(?:HandleFunc|Handle)\("([^"]*)",\s*([\w.]+)[\s\S]*?\.Methods\(([^)]*)\)`)

var methodLiteral = regexp.MustCompile(`"([A-Z]+)"`)

// routerPrefixes maps the subrouter variables of server.go to the path prefix
// they were created with.
var routerPrefixes = map[string]string{
	"r":             "",
	"api":           "/api/v1",
	"authRoutes":    "/api/v1/auth",
	"protected":     "/api/v1",
	"org":           "/api/v1/organizations/{org_slug}",
	"orgAdmin":      "/api/v1/organizations/{org_slug}",
	"platformAdmin": "/api/v1/platform-admin",
}

// ParseRegisteredRoutes reads the router setup source and returns the full paths
// it serves. It exists so the OpenAPI document can be checked against the router
// rather than against someone's memory of it.
func ParseRegisteredRoutes(source string) []RegisteredRoute {
	byPath := map[string]*RegisteredRoute{}

	for _, match := range routeLine.FindAllStringSubmatch(source, -1) {
		router, path, handler := match[1], match[2], match[3]
		prefix, known := routerPrefixes[router]
		if !known {
			continue
		}

		full := prefix + path
		if full == "" {
			continue
		}
		route, seen := byPath[full]
		if !seen {
			route = &RegisteredRoute{Path: full, Handlers: map[string]string{}}
			byPath[full] = route
		}
		for _, method := range methodLiteral.FindAllStringSubmatch(match[4], -1) {
			if _, already := route.Handlers[method[1]]; !already {
				route.Handlers[method[1]] = handler
				route.Methods = append(route.Methods, method[1])
			}
		}
	}

	routes := make([]RegisteredRoute, 0, len(byPath))
	for _, route := range byPath {
		sort.Strings(route.Methods)
		routes = append(routes, *route)
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].Path < routes[j].Path })
	return routes
}

// OpenAPIPath converts a gorilla/mux path to the OpenAPI form. They agree on
// {name}, but mux also allows a pattern after a colon.
func OpenAPIPath(path string) string {
	return regexp.MustCompile(`\{(\w+)(:[^}]*)?\}`).ReplaceAllString(path, "{$1}")
}

// PublicRoute reports whether a path is served without authentication, which is
// what the OpenAPI security block has to mirror.
func PublicRoute(path string) bool {
	switch {
	case path == "/healthz", path == "/readyz":
		return true
	case strings.HasPrefix(path, "/api/v1/auth/"):
		return true
	case strings.HasPrefix(path, "/api/v1/webhooks/"):
		return true
	case path == "/api/v1/contact", path == "/api/v1/unsubscribe", path == "/api/v1/status", path == "/api/v1/openapi.json":
		return true
	case strings.HasPrefix(path, "/api/v1/schemas/"):
		return true
	case strings.HasPrefix(path, "/api/v1/agents/"):
		return true
	default:
		return false
	}
}
