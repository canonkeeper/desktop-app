package auth

import (
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
)

// A session opened on behalf of a user (impersonation, UC5) is a child session: its cookie
// carries impersonationChildCookiePrefix and works only next to the pair cookie that names
// it. The core keeps this fence because every handler reads the session cookie through
// SessionCookieValue; who may open a child session is decided outside the core.
const (
	ImpersonatorCookieName         = "ck_impersonator"
	impersonationChildCookiePrefix = "imp_"
)

func ImpersonatorCookieValue(r *http.Request) string {
	cookie, err := r.Cookie(ImpersonatorCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func IsImpersonationChildCookie(value string) bool {
	return strings.HasPrefix(value, impersonationChildCookiePrefix)
}

// EncodeImpersonationPair is the pair cookie of a child session: the administrator's
// session cookie and the child's.
func EncodeImpersonationPair(adminCookieValue, childCookieValue string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(adminCookieValue + "\n" + childCookieValue))
}

func DecodeImpersonationPair(value string) (adminCookieValue, childCookieValue string, ok bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", "", false
	}
	parts := strings.Split(string(decoded), "\n")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func matchesImpersonationChildPair(childCookieValue, pairCookieValue string) bool {
	_, child, ok := DecodeImpersonationPair(pairCookieValue)
	return ok && subtle.ConstantTimeCompare([]byte(child), []byte(childCookieValue)) == 1
}
