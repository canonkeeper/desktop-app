package httpapi

import (
	"io"
	"net/http"
)

// MaxJSONBodyBytes bounds the JSON body of an operation whose handler sets no limit of its own
// (SEC-15, specs/2026-10-28-security-hardening.md R2.1): the longest such body, a project with a
// description of 1000 characters, is a few kilobytes; nginx alone lets 52 MB through.
const MaxJSONBodyBytes = 64 << 10

// JSONBody is the request body cut at MaxJSONBodyBytes: a longer body ends early and fails to
// decode like any broken JSON.
func JSONBody(r *http.Request) io.Reader {
	return io.LimitReader(r.Body, MaxJSONBodyBytes)
}
