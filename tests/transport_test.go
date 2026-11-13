//go:build !wasm

package tests

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pyr33x/wisp/shared"
	"github.com/pyr33x/wisp/transport"
)

func TestCall_DecodesSuccessAndReportsStatus(t *testing.T) {
	c := newClient(map[string]answer{
		"GET /ok":   {status: 200, body: `{"exists":true}`},
		"GET /miss": {status: 404, body: `{"error":{"code":"not_found","message":"m"}}`},
		"GET /down": {err: plainErr("boom")},
	})
	var e shared.Exists
	if st, err := transport.Call(c, "GET", "/ok", nil, &e); err != nil || st != 200 || !e.Exists {
		t.Fatalf("ok: %d %v %+v", st, err, e)
	}
	st, err := transport.Call(c, "GET", "/miss", nil, &e)
	se, isStatus := err.(*transport.StatusError)
	if st != 404 || !isStatus || se.Status != 404 {
		t.Fatalf("miss: %d %v", st, err)
	}
	if st, err := transport.Call(c, "GET", "/down", nil, nil); err == nil || st != 0 {
		t.Fatalf("down: %d %v", st, err)
	}
}

func TestCall_EncodesBody(t *testing.T) {
	c := newClient(map[string]answer{"POST /api/links": {status: 201}})
	if _, err := transport.Call(c, "POST", "/api/links", &shared.CreateLinkRequest{URL: "https://x.io"}, nil); err != nil {
		t.Fatal(err)
	}
	if c.calls[0].body != `{"url":"https://x.io"}` {
		t.Fatalf("body = %s", c.calls[0].body)
	}
}

// TestURLClient_SendsSecretAndBody runs the URL transport against a real HTTP
// server: the header and body that reach the internal Worker.
func TestURLClient_SendsSecretAndBody(t *testing.T) {
	var gotSecret, gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSecret = r.Header.Get(shared.HeaderInternal)
		gotMethod, gotPath = r.Method, r.URL.Path
		b := make([]byte, r.ContentLength)
		r.Body.Read(b)
		gotBody = string(b)
		w.WriteHeader(201)
		w.Write([]byte(`{"code":"abc1234"}`))
	}))
	defer srv.Close()

	c := transport.NewURLClient(srv.URL+"/", secret, transport.DefaultTimeoutMs)
	st, body, err := c.Do("POST", "/api/links", []byte(`{"url":"https://x.io"}`))
	if err != nil || st != 201 || string(body) != `{"code":"abc1234"}` {
		t.Fatalf("Do = %d %s %v", st, body, err)
	}
	if gotSecret != secret || gotMethod != "POST" || gotPath != "/api/links" || gotBody != `{"url":"https://x.io"}` {
		t.Fatalf("server saw %q %s %s %s", gotSecret, gotMethod, gotPath, gotBody)
	}
}

func TestURLClient_TimeoutIsAnError(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-block }))
	defer srv.Close()
	defer close(block)
	c := transport.NewURLClient(srv.URL, secret, 50)
	if _, _, err := c.Do("POST", "/api/clicks/abc1234", nil); err == nil {
		t.Fatal("a call past its timeout must fail")
	}
}
