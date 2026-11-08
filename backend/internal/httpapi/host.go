// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import "net/http"

// RequireHost answers 421 without a body to every request whose Host is not host — the loopback address and port the
// desktop listens on — so a page of another site that a DNS rebinding pointed at it reaches nothing
// (specs/2026-11-07-desktop-app.md DA3.4).
func RequireHost(host string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Host != host {
				w.WriteHeader(http.StatusMisdirectedRequest)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
