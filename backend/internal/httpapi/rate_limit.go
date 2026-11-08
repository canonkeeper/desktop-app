package httpapi

import (
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"canonkeeper/backend/internal/observability"
	"canonkeeper/backend/internal/ratelimit"
)

type RateLimitKeyOptions struct {
	PrincipalID func(*http.Request) string
	ProjectID   func(*http.Request) string
}

type RateLimitKeyFunc func(*http.Request) ratelimit.Key

func NewRateLimitKeyFunc(options RateLimitKeyOptions) RateLimitKeyFunc {
	return func(r *http.Request) ratelimit.Key {
		route := registeredRouteForRequest(r)
		operation := route.OperationID
		if operation == "" {
			operation = "unmatchedRoute"
		}

		projectID := ""
		if options.ProjectID != nil {
			projectID = options.ProjectID(r)
		}
		if projectID == "" {
			projectID = routePathParameter(route.Path, normalizedRoutePath(r.URL.Path), "projectId")
		}

		if options.PrincipalID != nil {
			if principalID := options.PrincipalID(r); principalID != "" {
				return ratelimit.AuthenticatedKey(principalID, projectID, operation)
			}
		}

		return ratelimit.AnonymousKey(operation, ClientAddress(r))
	}
}

// clientLoggedOperations are the anonymous operations whose request line keeps the client address:
// without it neither password guessing (sign-in, a reset link) nor mass registration and reset
// letters can be investigated. Any other guest request — a page, a CSRF token, an error report, a
// confirmation link — leaves no address: the privacy policy covers the address of someone who signs
// in or registers, not of a visitor (SEC-17, security-hardening R4.1).
var clientLoggedOperations = map[string]bool{
	"loginUser":            true,
	"registerUser":         true,
	"requestPasswordReset": true,
	"resetPassword":        true,
}

func RateLimitMiddleware(limiter ratelimit.Limiter, keyFunc RateLimitKeyFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyFunc(r)
			// Who asks goes to the request line, a turned-away request included: the user of a
			// session, or the address of an anonymous sign-in (SEC-17, security-hardening R4.1).
			if key.PrincipalID != ratelimit.AnonymousPrincipal {
				observability.ReportRequestUser(r.Context(), key.PrincipalID)
			} else if clientLoggedOperations[key.Operation] {
				observability.ReportRequestClient(r.Context(), ClientAddress(r))
			}
			decision, err := limiter.Allow(r.Context(), key)
			if err != nil {
				WriteProblem(w, r.Header.Get(observability.RequestIDHeader), http.StatusInternalServerError, "Internal Server Error", "rate limit unavailable", "rate_limit.error")
				return
			}
			if !decision.Allowed {
				WriteRateLimited(w, r.Header.Get(observability.RequestIDHeader), decision.RetryAfter)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// WriteRateLimited is the one 429 answer for every budget, so a caller cannot
// tell which budget (address, operation or account) ran out.
func WriteRateLimited(w http.ResponseWriter, requestID string, retryAfter time.Duration) {
	w.Header().Set("Retry-After", retryAfterHeader(retryAfter))
	WriteProblem(w, requestID, http.StatusTooManyRequests, "Too Many Requests", "rate limit exceeded", "rate_limit.exceeded")
}

func retryAfterHeader(duration time.Duration) string {
	if duration <= 0 {
		return "0"
	}
	seconds := duration / time.Second
	if duration%time.Second != 0 {
		seconds++
	}
	return strconv.FormatInt(int64(seconds), 10)
}

// ClientAddress is the address budgets are counted for. X-Real-IP is trusted only
// from a loopback peer: the local nginx sets it from the client address it received
// from Caddy (SEC-03). An IPv6 client counts by its /64, which one host can rotate
// through freely.
func ClientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	peer = peer.Unmap()
	if peer.IsLoopback() {
		if client, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Real-IP"))); err == nil {
			peer = client.Unmap()
		}
	}
	if peer.Is6() {
		return netip.PrefixFrom(peer, 64).Masked().String()
	}
	return peer.String()
}
