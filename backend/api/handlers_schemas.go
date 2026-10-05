package api

import (
	_ "embed"
	"net/http"
	"os"
	"path/filepath"
)

// webhookPayloadSchema describes the structured notification payload. It lives
// here because this is where the payload is produced: a schema kept in another
// repository cannot be checked against the code that emits it.
//
//go:embed schemas/webhook-payload.json
var webhookPayloadSchema []byte

// agentConfigSchema is owned by the agent repository and read from disk, the
// same way the agent binaries are served. A deployment that ships the API
// without the agent tree simply serves 404 here.
func agentConfigSchemaPath() (string, bool) {
	candidates := []string{
		"../agent/schemas/agent-config.json",
		"agent/schemas/agent-config.json",
	}
	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		candidates = append(candidates,
			filepath.Join(exeDir, "../agent/schemas/agent-config.json"),
			filepath.Join(exeDir, "agent/schemas/agent-config.json"))
	}
	for _, candidate := range candidates {
		absPath, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if info, err := os.Stat(absPath); err == nil && !info.IsDir() {
			return absPath, true
		}
	}
	return "", false
}

func serveSchema(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/schema+json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Write(body)
}

func handleWebhookPayloadSchema() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		serveSchema(w, webhookPayloadSchema)
	}
}

func handleAgentConfigSchema() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		path, found := agentConfigSchemaPath()
		if !found {
			respondError(w, http.StatusNotFound, ErrSchemaMissing)
			return
		}
		body, err := os.ReadFile(path)
		if err != nil {
			respondError(w, http.StatusInternalServerError, ErrSchemaMissing)
			return
		}
		serveSchema(w, body)
	}
}
