package transport

import (
	"webtyp.com/fetch"

	"github.com/pyr33x/wisp/shared"
)

// URLClient calls another Worker over its public URL, authenticating with the
// X-Wisp-Internal shared secret. It is the mode wisp runs in today: goflare
// cannot declare service bindings, so internal Workers stay reachable on
// workers.dev and reject every request without the secret.
type URLClient struct {
	baseURL   string
	secret    string
	timeoutMs int
}

// NewURLClient returns a client for baseURL (e.g. https://wisp-link.acct.workers.dev).
func NewURLClient(baseURL, secret string, timeoutMs int) *URLClient {
	return &URLClient{baseURL: shared.NormalizeBaseURL(baseURL), secret: secret, timeoutMs: timeoutMs}
}

func (c *URLClient) Do(method, path string, body []byte) (int, []byte, error) {
	var req *fetch.Request
	url := c.baseURL + path
	switch method {
	case MethodPost:
		req = fetch.Post(url)
	case MethodDelete:
		req = fetch.Delete(url)
	default:
		req = fetch.Get(url)
	}
	req = req.Header(shared.HeaderInternal, c.secret).Timeout(c.timeoutMs)
	if len(body) > 0 {
		req = req.ContentTypeJSON().Body(body)
	}

	type result struct {
		resp *fetch.Response
		err  error
	}
	// fetch is callback based; the handler runs in its own goroutine, so it
	// can block here while the JS promise settles.
	done := make(chan result, 1)
	req.Send(func(resp *fetch.Response, err error) {
		done <- result{resp, err}
	})
	r := <-done
	if r.err != nil {
		return 0, nil, r.err
	}
	if r.resp == nil {
		return 0, nil, ErrNoResponse
	}
	return r.resp.Status, r.resp.Body(), nil
}
