package api

import (
	_ "embed"
	"net/http"
)

// openAPIDocument is generated from the routes the router registers
// (scripts/gen-openapi.mjs). Serving it publicly is what lets a client be
// generated instead of hand-written.
//
//go:embed openapi.json
var openAPIDocument []byte

func handleOpenAPI() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.Write(openAPIDocument)
	}
}
