// Package transport is how wisp Workers call each other. Callers depend only on
// Client; whether a call travels over a workers.dev URL (with the
// X-Wisp-Internal shared secret) or over a Cloudflare service binding is a
// configuration choice made once in main (see FromEnv on wasm).
package transport

import (
	"webtyp.com/fmt"
	"webtyp.com/json"
	"webtyp.com/model"
)

// Client performs one internal HTTP call. path is absolute ("/api/links/abc1234").
// err is non-nil only when no HTTP response was obtained (network failure,
// timeout); any HTTP status, including 5xx, is returned in status.
type Client interface {
	Do(method, path string, body []byte) (status int, respBody []byte, err error)
}

// Methods used between services.
const (
	MethodGet    = "GET"
	MethodPost   = "POST"
	MethodDelete = "DELETE"
)

// Timeouts in milliseconds. Requests wait on I/O, not CPU, so they do not count
// against the 10 ms CPU budget; they only bound wall time.
const (
	// DefaultTimeoutMs bounds calls whose answer the request depends on.
	DefaultTimeoutMs = 5000
	// BestEffortTimeoutMs bounds the click recording on a redirect: the
	// redirect is never delayed by more than this.
	BestEffortTimeoutMs = 300
)

// ErrNoResponse wraps a call that produced no HTTP response.
var ErrNoResponse = fmt.Err("internal", "call", "got", "no", "response")

// StatusError is a non-2xx answer from a downstream service.
type StatusError struct {
	Method string
	Path   string
	Status int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s %s answered %d", e.Method, e.Path, e.Status)
}

// Call sends in (nil for no body) and decodes a 2xx answer into out (nil to
// ignore the body). It returns the status and, for a non-2xx answer, a
// *StatusError; out is left untouched then.
func Call(c Client, method, path string, in model.Encodable, out model.Decodable) (int, error) {
	var body []byte
	if in != nil {
		if err := json.Encode(in, &body); err != nil {
			return 0, err
		}
	}
	status, resp, err := c.Do(method, path, body)
	if err != nil {
		return 0, err
	}
	if status < 200 || status > 299 {
		return status, &StatusError{Method: method, Path: path, Status: status}
	}
	if out != nil && len(resp) > 0 {
		if err := json.Decode(resp, out); err != nil {
			return status, err
		}
	}
	return status, nil
}
