package tests

import (
	"sync"
	"testing"

	"webtyp.com/json"
	"webtyp.com/router"
	"webtyp.com/router/mock"
	"webtyp.com/storage"

	"github.com/pyr33x/wisp/platform"
	"github.com/pyr33x/wisp/shared"
)

// cryptoSource is the production random source for this build target.
var cryptoSource shared.RandomSource = platform.CryptoRandom

// ── logger ──────────────────────────────────────────────────────────────────

type logLine struct {
	status int
	detail string
}

type fakeLogger struct {
	mu    sync.Mutex
	fails []logLine
	rejs  []logLine
}

func (l *fakeLogger) Fail(status int, method, path string, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	l.fails = append(l.fails, logLine{status, msg})
}

func (l *fakeLogger) Reject(status int, method, path, reason string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rejs = append(l.rejs, logLine{status, reason})
}

// ── request driving ─────────────────────────────────────────────────────────

type request struct {
	method  string
	path    string
	body    string
	headers map[string]string
}

// serve registers routes on a mock.Router (which enforces the same gate and
// middleware order as the edge router) and drives one request through it.
func serve(register func(router.Router), req request) *mock.Context {
	r := &mock.Router{}
	register(r)
	ctx := &mock.Context{InMethod: req.method, InPath: req.path, InBody: []byte(req.body)}
	for k, v := range req.headers {
		ctx.SetHeader(k, v)
	}
	r.Invoke(req.method, req.path, ctx)
	return ctx
}

func wantStatus(t *testing.T, ctx *mock.Context, want int) {
	t.Helper()
	if ctx.Status != want {
		t.Fatalf("status = %d, want %d (body %s)", ctx.Status, want, ctx.ResponseBody())
	}
}

// wantError asserts the one error shape and its code.
func wantError(t *testing.T, ctx *mock.Context, status int, code string) {
	t.Helper()
	wantStatus(t, ctx, status)
	var e shared.ErrorBody
	if err := json.Decode(ctx.ResponseBody(), &e); err != nil {
		t.Fatalf("error body is not JSON: %v (%s)", err, ctx.ResponseBody())
	}
	if e.Error.Code != code {
		t.Fatalf("error code = %q, want %q", e.Error.Code, code)
	}
	if e.Error.Message == "" {
		t.Fatal("error message is empty")
	}
	if ct := ctx.GetHeader(shared.HeaderContentType); ct != shared.ContentTypeJSON {
		t.Fatalf("content-type = %q", ct)
	}
}

// ── random sources ──────────────────────────────────────────────────────────

// seqRandom returns a source that yields the given bytes in order, cycling.
func seqRandom(seq ...byte) shared.RandomSource {
	i := 0
	return func(b []byte) error {
		for k := range b {
			b[k] = seq[i%len(seq)]
			i++
		}
		return nil
	}
}

// ── storage.Executor fake ───────────────────────────────────────────────────

type execCall struct {
	query string
	args  []any
}

type scanFunc func(dest ...any) error

type scanner struct{ fn scanFunc }

func (s scanner) Scan(dest ...any) error { return s.fn(dest...) }

// fakeExec records every statement and answers through the configured funcs.
type fakeExec struct {
	mu       sync.Mutex
	execs    []execCall
	queries  []execCall
	onExec   func(query string, args []any) error
	onQuery  func(query string, args []any) scanFunc
}

func (f *fakeExec) Exec(query string, args ...any) (storage.Result, error) {
	f.mu.Lock()
	f.execs = append(f.execs, execCall{query, args})
	f.mu.Unlock()
	if f.onExec != nil {
		if err := f.onExec(query, args); err != nil {
			return storage.Affected(0), err
		}
	}
	return storage.Affected(1), nil
}

func (f *fakeExec) QueryRow(query string, args ...any) storage.Scanner {
	f.mu.Lock()
	f.queries = append(f.queries, execCall{query, args})
	f.mu.Unlock()
	if f.onQuery == nil {
		return scanner{func(...any) error { return storage.ErrNoRows }}
	}
	return scanner{f.onQuery(query, args)}
}

func (f *fakeExec) Query(query string, args ...any) (storage.Rows, error) {
	panic("wisp never runs multi-row queries")
}

func (f *fakeExec) Close() error { return nil }

// ── transport.Client fake ───────────────────────────────────────────────────

type answer struct {
	status int
	body   string
	err    error
}

type clientCall struct {
	method string
	path   string
	body   string
}

// fakeClient answers by "METHOD path"; an unknown call answers 500.
type fakeClient struct {
	mu      sync.Mutex
	answers map[string]answer
	calls   []clientCall
}

func newClient(answers map[string]answer) *fakeClient {
	return &fakeClient{answers: answers}
}

func (c *fakeClient) Do(method, path string, body []byte) (int, []byte, error) {
	c.mu.Lock()
	c.calls = append(c.calls, clientCall{method, path, string(body)})
	c.mu.Unlock()
	a, ok := c.answers[method+" "+path]
	if !ok {
		return 500, nil, nil
	}
	if a.err != nil {
		return 0, nil, a.err
	}
	return a.status, []byte(a.body), nil
}

func (c *fakeClient) called(method, path string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, k := range c.calls {
		if k.method == method && k.path == path {
			return true
		}
	}
	return false
}
