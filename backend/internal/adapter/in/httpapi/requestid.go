package httpapi

import (
	"context"
	"crypto/rand"
	"net/http"
	"regexp"
	"time"
)

// requestIDRe bounds an inbound X-Request-Id (only accepted from a trusted
// proxy): safe characters, 8–128 long, so it can never inject log lines.
var requestIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{8,128}$`)

type requestIDKey struct{}

// requestIDFrom returns the id placed in the context by requestID; "" when
// the middleware is not in the chain (bare handler unit tests).
func requestIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey{}).(string)
	return v
}

// crockford is the ULID alphabet (no I, L, O, U).
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// newRequestID returns a 26-char ULID-style id: 48-bit millisecond
// timestamp followed by 80 bits from crypto/rand, Crockford base32.
func newRequestID(now time.Time) string {
	var b [16]byte
	ms := uint64(now.UnixMilli())
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	// crypto/rand.Read never returns an error (Go 1.24+); it crashes the
	// program irrecoverably instead.
	_, _ = rand.Read(b[6:])
	return encodeULID(b)
}

// encodeULID renders 128 big-endian bits as 26 base32 symbols, least
// significant symbol last (the first symbol only carries 3 bits).
func encodeULID(b [16]byte) string {
	var out [26]byte
	acc, bits, j := uint64(0), uint(0), 25
	for i := 15; i >= 0; i-- {
		acc |= uint64(b[i]) << bits
		bits += 8
		for bits >= 5 && j >= 0 {
			out[j] = crockford[acc&31]
			acc >>= 5
			bits -= 5
			j--
		}
	}
	for j >= 0 {
		out[j] = crockford[acc&31]
		acc >>= 5
		j--
	}
	return string(out[:])
}

// requestID assigns every request an id: the inbound X-Request-Id when the
// peer is a trusted proxy and the value matches requestIDRe, else a fresh
// ULID. The id is echoed on the response and stored in the context for the
// logger and the error envelope.
func (s *server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := ""
		if s.proxy.peerTrusted(r) {
			if v := r.Header.Get("X-Request-Id"); requestIDRe.MatchString(v) {
				id = v
			}
		}
		if id == "" {
			id = newRequestID(time.Now())
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}
