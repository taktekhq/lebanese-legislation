package site

import (
	_ "embed"
	"net/http"
)

// openapiJSON describes /api/v1/ as OpenAPI 3.1, for code generators and AI
// agents. It is served at /openapi.json by the server and, through the static
// export, from R2 by the static site's Worker. Every route in apiEndpoints is
// in it (openapi_test.go).
//
//go:embed openapi.json
var openapiJSON []byte

// updatedAtNote is what an updated_at field means; the API root repeats it.
const updatedAtNote = "updated_at is the day a record's page last changed on the site (an amendment in force, a ruling's reading, a correction), never the text's own date; absent when not known."

func (s *Server) openapi(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("Access-Control-Allow-Origin", "*") // API explorers read it from other sites
	_, _ = w.Write(openapiJSON)
}
