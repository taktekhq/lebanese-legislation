package site

import (
	"log"
	"net/http"
)

// logError writes a failed request to the server log: what failed (kind),
// the HTTP method, the route pattern when the mux matched one, the address
// asked for, and the error text. The container log is where errors are read.
func logError(r *http.Request, kind string, err any) {
	if r.Pattern != "" {
		log.Printf("%s %s %s (route %s): %v", kind, r.Method, r.URL.RequestURI(), r.Pattern, err)
		return
	}
	log.Printf("%s %s %s: %v", kind, r.Method, r.URL.RequestURI(), err)
}
