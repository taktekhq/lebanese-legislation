package site

import (
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Scanned articles are pictures on the Lebanese University's server, which
// serves them over plain HTTP only (HTTPS answers 500). An HTTPS page cannot
// show them directly, so the site fetches them itself under /ul/uploads/images/
// and marks them cacheable for a year: Cloudflare keeps them, UL is asked once.
const ulImages = "https://legallaw.ul.edu.lb/uploads/images/"

// A few UL records point their pictures at the Centre's internal address,
// which no reader can reach. The same files are on the public server.
const ulInternalImages = "http://10.135.0.250/uploads/images/"

var ulClient = &http.Client{Timeout: 30 * time.Second}

// imageURL maps a stored picture address to the one pages use.
func imageURL(u string) string {
	for _, prefix := range []string{ulImages, ulInternalImages} {
		if strings.HasPrefix(u, prefix) {
			return "/ul/uploads/images/" + strings.TrimPrefix(u, prefix)
		}
	}
	return u
}

// ulImageName is the shape of a picture name at UL: path segments of
// letters (Arabic too), digits, "_", "-", "(", ")" and dots, none starting
// with a dot. Escapes ("%"), backslashes and ".." never pass.
var ulImageName = regexp.MustCompile(`^[\p{L}\p{N}_()-][\p{L}\p{N}._()-]*(/[\p{L}\p{N}_()-][\p{L}\p{N}._()-]*)*$`)

// ulImageTypes are the picture types relayed: raster images only, never
// anything a browser could run (SVG) or render as a page.
var ulImageTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true, "image/bmp": true, "image/x-ms-bmp": true}

func (s *Server) ulImage(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !ulImageName.MatchString(name) || strings.Contains(name, "..") {
		http.NotFound(w, r)
		return
	}
	resp, err := ulClient.Get(ulImages + name)
	if err != nil {
		http.Error(w, "The picture could not be fetched from the Lebanese University.", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	ct, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if resp.StatusCode != http.StatusOK || err != nil || !ulImageTypes[ct] {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	io.Copy(w, io.LimitReader(resp.Body, 15<<20))
}
