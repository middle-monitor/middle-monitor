package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

type openAPIDoc struct {
	OpenAPI string `json:"openapi"`
	Info    struct {
		Title   string `json:"title"`
		Version string `json:"version"`
	} `json:"info"`
	Paths      map[string]map[string]json.RawMessage `json:"paths"`
	Components struct {
		Schemas map[string]json.RawMessage `json:"schemas"`
	} `json:"components"`
}

func loadSpec(t *testing.T) openAPIDoc {
	t.Helper()
	var doc openAPIDoc
	if err := json.Unmarshal(openAPIDocument, &doc); err != nil {
		t.Fatalf("openapi.json is not valid JSON: %v", err)
	}
	return doc
}

// The reason to publish a specification at all: it has to describe the API that
// is actually served. A route added without regenerating the document would
// otherwise be invisible to every generated client.
func TestOpenAPICoversEveryRoute(t *testing.T) {
	source, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}
	doc := loadSpec(t)

	var missing []string
	for _, route := range ParseRegisteredRoutes(string(source)) {
		path := OpenAPIPath(route.Path)
		operations, described := doc.Paths[path]
		if !described {
			missing = append(missing, path)
			continue
		}
		for _, method := range route.Methods {
			if _, ok := operations[strings.ToLower(method)]; !ok {
				missing = append(missing, method+" "+path)
			}
		}
	}

	if len(missing) > 0 {
		t.Fatalf("openapi.json does not describe %d served operations (run scripts/gen-openapi.mjs): %v",
			len(missing), missing)
	}
}

// The reverse: a path in the document that nothing serves sends a generated
// client at an endpoint that answers 404.
func TestOpenAPIDescribesNothingItDoesNotServe(t *testing.T) {
	source, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}

	served := map[string]bool{}
	for _, route := range ParseRegisteredRoutes(string(source)) {
		served[OpenAPIPath(route.Path)] = true
	}

	for path := range loadSpec(t).Paths {
		if !served[path] {
			t.Fatalf("openapi.json describes %s, which the router does not serve", path)
		}
	}
}

// A tool reading the specification has to find the resources it manipulates
// described, not just listed as opaque objects.
func TestOpenAPIDescribesTheResourcesAToolWrites(t *testing.T) {
	doc := loadSpec(t)
	for _, schema := range []string{
		"Host", "HostGroup", "Incident", "IncidentStatusUpdate",
		"AlertRule", "NotificationChannel", "WebhookChannelConfig", "WebhookDelivery", "AgentRelease",
	} {
		if _, present := doc.Components.Schemas[schema]; !present {
			t.Fatalf("schema %s is missing from openapi.json", schema)
		}
	}
	if doc.OpenAPI == "" || doc.Info.Title == "" {
		t.Fatalf("incomplete document header: %+v", doc.Info)
	}
}

// The document is useless behind authentication: the client generator, and the
// agent reading it, have no credentials.
func TestOpenAPIIsServedPublicly(t *testing.T) {
	rec := httptest.NewRecorder()
	handleOpenAPI()(rec, httptest.NewRequest("GET", "/api/v1/openapi.json", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type %q", got)
	}
	if !PublicRoute("/api/v1/openapi.json") {
		t.Fatal("the specification must not require credentials")
	}
}

// Route parsing is what both tests above rely on, so its own reading of
// server.go has to be right about prefixes and methods.
func TestRouteParsingResolvesSubrouterPrefixes(t *testing.T) {
	source := `
	api.HandleFunc("/agents/latest", handleAgentLatest()).Methods("GET")
	org.HandleFunc("/hosts", handleGetHosts(db)).Methods("GET")
	org.HandleFunc("/hosts", handleSubmitHost(db)).Methods("POST")
	orgAdmin.HandleFunc("/users/{id}", handleUpdateUserRole(a)).Methods("PUT")
	r.HandleFunc("/healthz", liveHandler).Methods("GET")
	somethingElse.HandleFunc("/ignored", h).Methods("GET")
`
	routes := ParseRegisteredRoutes(source)
	if len(routes) != 4 {
		t.Fatalf("parsed %d routes, want 4: %+v", len(routes), routes)
	}

	byPath := map[string]RegisteredRoute{}
	for _, route := range routes {
		byPath[route.Path] = route
	}

	hosts, ok := byPath["/api/v1/organizations/{org_slug}/hosts"]
	if !ok {
		t.Fatalf("org prefix not applied: %+v", byPath)
	}
	if len(hosts.Methods) != 2 {
		t.Fatalf("methods %v, want GET and POST on one path", hosts.Methods)
	}
	if hosts.Handlers["POST"] != "handleSubmitHost" {
		t.Fatalf("handler %q", hosts.Handlers["POST"])
	}
	if _, ok := byPath["/healthz"]; !ok {
		t.Fatalf("root route lost: %+v", byPath)
	}
}

// The schemas the documentation points at have to be served, and served
// publicly: a receiver validating a payload has no credentials.
func TestSchemasAreServed(t *testing.T) {
	rec := httptest.NewRecorder()
	handleWebhookPayloadSchema()(rec, httptest.NewRequest("GET", "/api/v1/schemas/webhook-payload.json", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/schema+json" {
		t.Fatalf("content-type %q", got)
	}

	var schema map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &schema); err != nil {
		t.Fatalf("the served schema is not valid JSON: %v", err)
	}
	if schema["$id"] != "https://middlemonitor.io/schemas/webhook-payload.json" {
		t.Fatalf("$id is %v, which is the URL receivers reference", schema["$id"])
	}
}

// The agent schema is owned by the agent repository and read from disk. A
// deployment without the agent tree answers 404 rather than serving nothing
// with a 200, which is the failure mode the whole 404 work was about.
func TestAgentSchemaIsServedOrHonestly404(t *testing.T) {
	rec := httptest.NewRecorder()
	handleAgentConfigSchema()(rec, httptest.NewRequest("GET", "/api/v1/schemas/agent-config.json", nil))

	switch rec.Code {
	case http.StatusOK:
		var schema map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &schema); err != nil {
			t.Fatalf("the served schema is not valid JSON: %v", err)
		}
	case http.StatusNotFound:
		// The agent tree is not next to this checkout; nothing else to assert.
	default:
		t.Fatalf("status %d", rec.Code)
	}
}

// The agent schema is read from disk, so it has to be in the image that serves
// it. It answered 404 in production because the Dockerfile copied agent/dist and
// nothing else, and the handler's fallback made that look deliberate.
func TestImagesShipTheAgentSchema(t *testing.T) {
	dockerfile, err := os.ReadFile("../Dockerfile")
	if err != nil {
		t.Skipf("no Dockerfile next to this checkout: %v", err)
	}
	body := string(dockerfile)

	dist := strings.Count(body, "COPY --from=agent-builder /agent/dist")
	schemas := strings.Count(body, "COPY --from=agent-builder /agent/schemas")
	if dist == 0 {
		t.Fatal("the Dockerfile no longer copies the agent binaries; this test needs updating")
	}
	if schemas != dist {
		t.Fatalf("%d image(s) copy agent/dist but only %d copy agent/schemas: "+
			"/api/v1/schemas/agent-config.json will answer 404 there", dist, schemas)
	}
}
