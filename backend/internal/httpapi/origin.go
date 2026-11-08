package httpapi

import (
	"net/http"
	"strings"
)

func StrictOriginMiddleware(allowedOrigins ...string) func(http.Handler) http.Handler {
	return strictOriginMiddleware(func(r *http.Request) bool {
		return RequiresCSRF(r.Method)
	}, false, allowedOrigins...)
}

func StrictOriginRequiredMiddleware(allowedOrigins ...string) func(http.Handler) http.Handler {
	return strictOriginMiddleware(func(*http.Request) bool {
		return true
	}, true, allowedOrigins...)
}

func strictOriginMiddleware(requiresOrigin func(*http.Request) bool, allowSameOriginFetchMetadata bool, allowedOrigins ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowed[origin] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !requiresOrigin(r) {
				next.ServeHTTP(w, r)
				return
			}
			origin := r.Header.Get("Origin")
			if origin == "" && allowSameOriginFetchMetadata && isSameOriginFetch(r) {
				next.ServeHTTP(w, r)
				return
			}
			if _, ok := allowed[origin]; !ok {
				w.Header().Set("Cache-Control", "no-store")
				WriteProblem(w, "", http.StatusForbidden, "Forbidden", "invalid request origin", "origin.invalid")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isSameOriginFetch(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "same-origin")
}

// CrossSiteFetch reports a browser request that a page of another site started (Fetch Metadata):
// a link, a redirect or a form there. A typed address ("none") and a client that sends no Fetch
// Metadata are not one.
func CrossSiteFetch(r *http.Request) bool {
	site := r.Header.Get("Sec-Fetch-Site")
	return strings.EqualFold(site, "cross-site") || strings.EqualFold(site, "same-site")
}
