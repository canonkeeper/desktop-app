package httpapi

import (
	"net/http"
	"testing"
)

func TestRequiresCSRF(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if !RequiresCSRF(method) {
			t.Fatalf("RequiresCSRF(%q) = false, want true", method)
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		if RequiresCSRF(method) {
			t.Fatalf("RequiresCSRF(%q) = true, want false", method)
		}
	}
}
