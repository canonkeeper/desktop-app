// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// embeddedWeb is the desktop build of the web, copied into web/ before the backend is built; without it the page
// answers 503 and the API works as before.
//
//go:embed web
var embeddedWeb embed.FS

// pageHeaders are the headers of the site without HSTS (DA3.6): the page loads nothing from outside, no frame shows it
// and it sends no referrer.
var pageHeaders = map[string]string{
	"Content-Security-Policy": "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; " +
		"font-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'",
	"X-Frame-Options":        "DENY",
	"X-Content-Type-Options": "nosniff",
	"Referrer-Policy":        "no-referrer",
	"Permissions-Policy":     "camera=(), microphone=(), geolocation=(), payment=()",
}

// withPageHeaders sets pageHeaders on every answer of the backend, the API's too.
func withPageHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for name, value := range pageHeaders {
			w.Header().Set(name, value)
		}
		next.ServeHTTP(w, r)
	})
}

// webFiles are the files of the embedded build.
func webFiles() fs.FS {
	files, err := fs.Sub(embeddedWeb, "web")
	if err != nil {
		panic(err)
	}
	return files
}

// page serves the embedded build: a file of it as it is — the hashed assets for a year, the rest revalidated — and any
// other path the index, whose router shows the page of the address (/desktop/open among them, DA3).
func page(files fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" || hidden(name) || name == "README.md" || !isFile(files, name) {
			name = "index.html"
		}
		if !isFile(files, name) {
			http.Error(w, "the page of the desktop build is not built", http.StatusServiceUnavailable)
			return
		}
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.ServeFileFS(w, r, files, name)
	})
}

// hidden names a file under a directory or with a name that starts with a dot: the build's own, not the page's.
func hidden(name string) bool {
	return strings.HasPrefix(name, ".") || strings.Contains(name, "/.")
}

func isFile(files fs.FS, name string) bool {
	info, err := fs.Stat(files, name)
	return err == nil && !info.IsDir()
}
