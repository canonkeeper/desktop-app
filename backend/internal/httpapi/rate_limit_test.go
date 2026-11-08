package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// SEC-03 / R1.2: behind nginx every request comes from loopback, so the client
// address is taken from X-Real-IP, and only when the peer itself is loopback.
func TestRateLimitClientAddressTrustsRealIPOnlyFromLoopback(t *testing.T) {
	cases := []struct {
		name          string
		remoteAddr    string
		realIP        string
		forwardedFor  string
		wantBucketKey string
	}{
		{name: "loopback proxy", remoteAddr: "127.0.0.1:41000", realIP: "203.0.113.7", wantBucketKey: "ip:203.0.113.7"},
		{name: "loopback network", remoteAddr: "127.0.0.42:41000", realIP: "198.51.100.42", wantBucketKey: "ip:198.51.100.42"},
		{name: "ipv6 loopback proxy", remoteAddr: "[::1]:41000", realIP: "2001:db8::7", wantBucketKey: "ip:2001:db8::/64"},
		{name: "ipv6 client keeps its /64", remoteAddr: "127.0.0.1:41000", realIP: "2001:db8:0:0:aaaa:bbbb:cccc:dddd", wantBucketKey: "ip:2001:db8::/64"},
		{name: "mapped client address", remoteAddr: "127.0.0.1:41000", realIP: "::ffff:203.0.113.7", wantBucketKey: "ip:203.0.113.7"},
		{name: "direct peer spoofs header", remoteAddr: "198.51.100.9:41000", realIP: "203.0.113.7", wantBucketKey: "ip:198.51.100.9"},
		{name: "direct ipv6 peer", remoteAddr: "[2001:db8:1::5]:41000", wantBucketKey: "ip:2001:db8:1::/64"},
		{name: "invalid header", remoteAddr: "127.0.0.1:41000", realIP: "not-an-ip", wantBucketKey: "ip:127.0.0.1"},
		{name: "list in header", remoteAddr: "127.0.0.1:41000", realIP: "203.0.113.7, 198.51.100.1", wantBucketKey: "ip:127.0.0.1"},
		{name: "no header", remoteAddr: "127.0.0.1:41000", wantBucketKey: "ip:127.0.0.1"},
		{name: "forwarded-for is not trusted", remoteAddr: "127.0.0.1:41000", forwardedFor: "203.0.113.7", wantBucketKey: "ip:127.0.0.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.realIP != "" {
				req.Header.Set("X-Real-IP", tc.realIP)
			}
			if tc.forwardedFor != "" {
				req.Header.Set("X-Forwarded-For", tc.forwardedFor)
			}

			key := NewRateLimitKeyFunc(RateLimitKeyOptions{})(req)

			if key.BucketID != tc.wantBucketKey {
				t.Fatalf("BucketID = %q, want %q", key.BucketID, tc.wantBucketKey)
			}
		})
	}
}
