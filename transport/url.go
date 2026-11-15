package transport

import "github.com/pyr33x/wisp/shared"

// URLClient calls another Worker over its public URL, authenticating with the
// X-Wisp-Internal shared secret. It is the mode wisp runs in today: goflare
// cannot declare service bindings, so internal Workers stay reachable on
// workers.dev and reject every request without the secret.
//
// Do is implemented per target: url_wasm.go calls the Workers runtime's fetch
// directly, url_native.go uses webtyp.com/fetch for native builds and tests.
type URLClient struct {
	baseURL   string
	secret    string
	timeoutMs int
}

// NewURLClient returns a client for baseURL (e.g. https://wisp-link.acct.workers.dev).
func NewURLClient(baseURL, secret string, timeoutMs int) *URLClient {
	return &URLClient{baseURL: shared.NormalizeBaseURL(baseURL), secret: secret, timeoutMs: timeoutMs}
}
