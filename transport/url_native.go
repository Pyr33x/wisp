//go:build !wasm

package transport

import (
	"webtyp.com/fetch"

	"github.com/pyr33x/wisp/shared"
)

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
