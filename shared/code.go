package shared

import "webtyp.com/fmt"

// Short codes: 7 characters of base62. 62^7 ≈ 3.5e12 codes, so a collision
// is rare, and the caller retries on one anyway.
const (
	CodeLength   = 7
	CodeAlphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	// MaxCodeAttempts is how many codes link tries before it gives up on a create.
	MaxCodeAttempts = 5

	// codeRejectAbove makes byte → symbol mapping unbiased: 248 = 4*62 is the
	// largest multiple of 62 that fits in a byte, so bytes 248..255 are dropped
	// instead of folding onto the first 8 symbols.
	codeRejectAbove = 248
	alphabetSize    = 62
)

// RandomSource fills b with cryptographically secure random bytes.
// platform.CryptoRandom is the production one.
type RandomSource func(b []byte) error

// ErrRandom is returned when the random source fails or never yields enough
// usable bytes.
var ErrRandom = fmt.Err("random", "source", "failed")

// NewCode returns a fresh code drawn from rnd, with rejection sampling so every
// symbol is equally likely.
func NewCode(rnd RandomSource) (string, error) {
	var out [CodeLength]byte
	n := 0
	// Each round asks for a few spare bytes: on average 3% are rejected, so
	// one round almost always suffices. The round limit only stops a broken
	// source from looping forever.
	var buf [CodeLength * 2]byte
	for round := 0; round < 8 && n < CodeLength; round++ {
		if err := rnd(buf[:]); err != nil {
			return "", ErrRandom
		}
		for _, b := range buf {
			if b >= codeRejectAbove {
				continue
			}
			out[n] = CodeAlphabet[int(b)%alphabetSize]
			n++
			if n == CodeLength {
				break
			}
		}
	}
	if n < CodeLength {
		return "", ErrRandom
	}
	return string(out[:]), nil
}

// ValidCode reports whether s has the shape of a code. Every service checks
// it before touching storage, so a malformed path never costs a query.
func ValidCode(s string) bool {
	if len(s) != CodeLength {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			return false
		}
	}
	return true
}

// SecureEqual compares a and b in time that depends only on their lengths,
// never on where they first differ. Used for INTERNAL_SECRET and ADMIN_TOKEN.
// A length mismatch returns false immediately: secrets have a fixed length,
// so it leaks nothing an attacker can use.
func SecureEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
