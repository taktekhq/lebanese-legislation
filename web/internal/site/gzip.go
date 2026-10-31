package site

import (
	"compress/gzip"
	"net/http"
	"strings"
	"sync"
)

var gzPool = sync.Pool{New: func() any { w, _ := gzip.NewWriterLevel(nil, gzip.BestSpeed); return w }}

type gzWriter struct {
	http.ResponseWriter
	gz *gzip.Writer
}

func (w gzWriter) Write(b []byte) (int, error) { return w.gz.Write(b) }

func Compress(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			h.ServeHTTP(w, r)
			return
		}
		gz := gzPool.Get().(*gzip.Writer)
		defer gzPool.Put(gz)
		gz.Reset(w)
		defer gz.Close()
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		w.Header().Del("Content-Length")
		h.ServeHTTP(gzWriter{w, gz}, r)
	})
}
