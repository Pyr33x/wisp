//go:build wasm

package tests

// These tests run in a real browser (gotest → wasmbrowsertest). They install a
// fake Cloudflare context.env — env vars plus a D1 binding that speaks the D1
// JS API (prepare/bind/run/raw) over Go maps — and drive each Worker through
// the real webtyp/cloudflare stack: d1.NewEdge, env.Lookup, the edge router
// and edge.Dispatch. Nothing below the JS boundary is faked.

import (
	"strings"
	"syscall/js"
	"testing"

	"webtyp.com/cloudflare/d1"
	"webtyp.com/cloudflare/edge"
	"webtyp.com/env"
	"webtyp.com/json"
	"webtyp.com/router"
	"webtyp.com/router/mock"

	"github.com/pyr33x/wisp/analytics"
	"github.com/pyr33x/wisp/gateway"
	"github.com/pyr33x/wisp/link"
	"github.com/pyr33x/wisp/platform"
	"github.com/pyr33x/wisp/shared"
	"github.com/pyr33x/wisp/transport"
)

// ── fake D1 ─────────────────────────────────────────────────────────────────

type fakeLinkRow struct {
	origin    string
	createdAt float64
	deleted   bool
}

// fakeD1 implements exactly the statements wisp issues, with SQLite's
// semantics for them: UNIQUE violations reject with D1's message, soft-deleted
// rows are invisible to the active reads, upserts are atomic.
type fakeD1 struct {
	links  map[string]*fakeLinkRow
	clicks map[string]float64
	limits map[string]float64
	sql    []string
	funcs  []js.Func
}

func newFakeD1() *fakeD1 {
	return &fakeD1{links: map[string]*fakeLinkRow{}, clicks: map[string]float64{}, limits: map[string]float64{}}
}

func (f *fakeD1) fn(g func(this js.Value, args []js.Value) any) js.Func {
	fn := js.FuncOf(g)
	f.funcs = append(f.funcs, fn)
	return fn
}

func (f *fakeD1) release() {
	for _, fn := range f.funcs {
		fn.Release()
	}
}

func resolved(v any) js.Value { return js.Global().Get("Promise").Call("resolve", v) }

func rejected(msg string) js.Value {
	return js.Global().Get("Promise").Call("reject", js.Global().Get("Error").New(msg))
}

func jsArray(items ...any) js.Value { return js.ValueOf(items) }

func runResult(changes int) js.Value {
	meta := js.Global().Get("Object").New()
	meta.Set("changes", changes)
	res := js.Global().Get("Object").New()
	res.Set("meta", meta)
	return res
}

// binding returns the JS object installed as env.DB.
func (f *fakeD1) binding() js.Value {
	db := js.Global().Get("Object").New()
	db.Set("prepare", f.fn(func(_ js.Value, a []js.Value) any {
		query := a[0].String()
		f.sql = append(f.sql, query)
		stmt := js.Global().Get("Object").New()
		var args []js.Value
		stmt.Set("bind", f.fn(func(_ js.Value, b []js.Value) any {
			args = b
			return stmt
		}))
		stmt.Set("run", f.fn(func(js.Value, []js.Value) any { return f.run(query, args) }))
		stmt.Set("raw", f.fn(func(js.Value, []js.Value) any { return f.raw(query, args) }))
		return stmt
	}))
	return db
}

func (f *fakeD1) run(q string, a []js.Value) js.Value {
	switch {
	case strings.HasPrefix(q, "INSERT INTO links"):
		code := a[0].String()
		if _, ok := f.links[code]; ok {
			return rejected("D1_ERROR: UNIQUE constraint failed: links.code: SQLITE_CONSTRAINT")
		}
		f.links[code] = &fakeLinkRow{origin: a[1].String(), createdAt: a[2].Float()}
		return resolved(runResult(1))
	case strings.HasPrefix(q, "UPDATE links SET deleted_at"):
		if r, ok := f.links[a[1].String()]; ok && !r.deleted {
			r.deleted = true
			return resolved(runResult(1))
		}
		return resolved(runResult(0))
	case strings.HasPrefix(q, "INSERT INTO analytics") && strings.Contains(q, "ON CONFLICT(code) DO UPDATE"):
		f.clicks[a[0].String()]++
		return resolved(runResult(1))
	case strings.HasPrefix(q, "DELETE FROM rate_limits"):
		return resolved(runResult(0))
	}
	return rejected("fake D1: unexpected run: " + q)
}

func (f *fakeD1) raw(q string, a []js.Value) js.Value {
	switch {
	case strings.HasPrefix(q, "SELECT origin, created_at FROM links"):
		r, ok := f.links[a[0].String()]
		if !ok || r.deleted {
			return resolved(jsArray(jsArray("origin", "created_at")))
		}
		return resolved(jsArray(jsArray("origin", "created_at"), jsArray(r.origin, r.createdAt)))
	case strings.HasPrefix(q, "SELECT 1 FROM links"):
		r, ok := f.links[a[0].String()]
		if !ok || r.deleted {
			return resolved(jsArray(jsArray("1")))
		}
		return resolved(jsArray(jsArray("1"), jsArray(1)))
	case strings.HasPrefix(q, "SELECT clicks FROM analytics"):
		n, ok := f.clicks[a[0].String()]
		if !ok {
			return resolved(jsArray(jsArray("clicks")))
		}
		return resolved(jsArray(jsArray("clicks"), jsArray(n)))
	case strings.HasPrefix(q, "INSERT INTO rate_limits") && strings.HasSuffix(q, "RETURNING count"):
		key := a[0].String() + "|" + a[1].String()
		f.limits[key]++
		return resolved(jsArray(jsArray("count"), jsArray(f.limits[key])))
	}
	return rejected("fake D1: unexpected raw: " + q)
}

// installEnv sets the fake context.env the Workers runtime would provide.
func installEnv(t *testing.T, vars map[string]string, bindings map[string]js.Value) {
	t.Helper()
	e := js.Global().Get("Object").New()
	for k, v := range vars {
		e.Set(k, v)
	}
	for k, v := range bindings {
		e.Set(k, v)
	}
	ctx := js.Global().Get("Object").New()
	ctx.Set("env", e)
	js.Global().Set("context", ctx)
	t.Cleanup(func() { js.Global().Delete("context") })
}

// dispatch drives one request through the real edge router pipeline.
func dispatch(r router.Router, req request) *mock.Context {
	ctx := &mock.Context{InMethod: req.method, InPath: req.path, InBody: []byte(req.body)}
	for k, v := range req.headers {
		ctx.SetHeader(k, v)
	}
	edge.Dispatch(r, ctx)
	return ctx
}

func edgeRouter(register func(router.Router)) router.Router {
	r := edge.NewRouter(edge.Config{})
	register(r)
	edge.Validate(r)
	edge.ExportCompile(r) // what edge.Serve does before the first request
	return r
}

// ── platform ────────────────────────────────────────────────────────────────

func TestWasm_CryptoRandomUsesGetRandomValues(t *testing.T) {
	var b [64]byte
	if err := platform.CryptoRandom(b[:]); err != nil {
		t.Fatal(err)
	}
	zero := true
	for _, x := range b {
		if x != 0 {
			zero = false
		}
	}
	if zero {
		t.Fatal("64 random bytes were all zero")
	}
	code, err := shared.NewCode(platform.CryptoRandom)
	if err != nil || !shared.ValidCode(code) {
		t.Fatalf("code %q err %v", code, err)
	}
	if platform.NowMillis() < 1_600_000_000_000 {
		t.Fatal("NowMillis is not unix milliseconds")
	}
}

func TestWasm_EnvReadsContextEnv(t *testing.T) {
	installEnv(t, map[string]string{"BASE_URL": "https://wisp.example.com"}, nil)
	if env.Get("BASE_URL") != "https://wisp.example.com" {
		t.Fatalf("BASE_URL = %q", env.Get("BASE_URL"))
	}
	if _, ok := env.Lookup("ADMIN_TOKEN"); ok {
		t.Fatal("an unset key must not be found")
	}
}

// ── link over the real D1 adapter ──────────────────────────────────────────

func TestWasm_LinkLifecycleOverD1(t *testing.T) {
	fake := newFakeD1()
	defer fake.release()
	installEnv(t, map[string]string{"BASE_URL": "https://wisp.example.com/", "INTERNAL_SECRET": secret},
		map[string]js.Value{"DB": fake.binding()})

	db, err := d1.NewEdge("DB")
	if err != nil {
		t.Fatal(err)
	}
	svc := &link.Service{Store: &link.SQLStore{DB: db.RawConn()}, BaseURL: env.Get("BASE_URL"),
		Random: platform.CryptoRandom, Now: platform.NowMillis, Log: &fakeLogger{}, Secret: env.Get("INTERNAL_SECRET")}
	r := edgeRouter(svc.Register)

	wantError(t, dispatch(r, request{method: "GET", path: "/api/links/abc1234"}), 401, shared.ErrCodeUnauthorized)

	ctx := dispatch(r, request{method: "POST", path: "/api/links", body: `{"url":"https://example.org/x"}`, headers: internalHeader})
	wantStatus(t, ctx, 201)
	var l shared.Link
	if err := json.Decode(ctx.ResponseBody(), &l); err != nil {
		t.Fatal(err)
	}
	if l.ShortURL != "https://wisp.example.com/"+l.Code || fake.links[l.Code] == nil {
		t.Fatalf("created %+v", l)
	}

	ctx = dispatch(r, request{method: "GET", path: "/api/links/" + l.Code, headers: internalHeader})
	wantStatus(t, ctx, 200)
	var got shared.Link
	if err := json.Decode(ctx.ResponseBody(), &got); err != nil || got.Origin != "https://example.org/x" || got.CreatedAt != l.CreatedAt {
		t.Fatalf("resolved %+v %v", got, err)
	}

	wantStatus(t, dispatch(r, request{method: "DELETE", path: "/api/links/" + l.Code, headers: internalHeader}), 204)
	wantStatus(t, dispatch(r, request{method: "DELETE", path: "/api/links/" + l.Code, headers: internalHeader}), 204)
	wantError(t, dispatch(r, request{method: "GET", path: "/api/links/" + l.Code, headers: internalHeader}), 404, shared.ErrCodeNotFound)

	ctx = dispatch(r, request{method: "GET", path: "/api/links/" + l.Code + "/exists", headers: internalHeader})
	var ex shared.Exists
	if err := json.Decode(ctx.ResponseBody(), &ex); err != nil || ex.Exists {
		t.Fatalf("deleted link exists=%v %v", ex.Exists, err)
	}
}

func TestWasm_LinkRetriesRealUniqueViolation(t *testing.T) {
	fake := newFakeD1()
	defer fake.release()
	installEnv(t, nil, map[string]js.Value{"DB": fake.binding()})
	fake.links["1111111"] = &fakeLinkRow{origin: "https://taken.io"}

	db, err := d1.NewEdge("DB")
	if err != nil {
		t.Fatal(err)
	}
	rnd := seqRandom(1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3)
	svc := &link.Service{Store: &link.SQLStore{DB: db.RawConn()}, BaseURL: "https://w.io", Random: rnd,
		Now: platform.NowMillis, Log: &fakeLogger{}, Secret: secret}
	ctx := dispatch(edgeRouter(svc.Register), request{method: "POST", path: "/api/links", body: `{"url":"https://example.org"}`, headers: internalHeader})
	wantStatus(t, ctx, 201)
	if fake.links["3333333"] == nil || fake.links["1111111"].origin != "https://taken.io" {
		t.Fatal("D1's UNIQUE error must be recognised as a duplicate and retried")
	}
}

// ── analytics over the real D1 adapter ─────────────────────────────────────

func TestWasm_AnalyticsCountsOverD1(t *testing.T) {
	fake := newFakeD1()
	defer fake.release()
	installEnv(t, nil, map[string]js.Value{"DB": fake.binding()})
	db, err := d1.NewEdge("DB")
	if err != nil {
		t.Fatal(err)
	}
	svc := &analytics.Service{Store: &analytics.SQLStore{DB: db.RawConn()}, Log: &fakeLogger{}, Secret: secret}
	r := edgeRouter(svc.Register)

	ctx := dispatch(r, request{method: "GET", path: "/api/stats/abc1234", headers: internalHeader})
	var st shared.Stats
	if err := json.Decode(ctx.ResponseBody(), &st); err != nil || st.Clicks != 0 {
		t.Fatalf("no row must be 0: %+v %v", st, err)
	}
	for i := 0; i < 2; i++ {
		wantStatus(t, dispatch(r, request{method: "POST", path: "/api/clicks/abc1234", headers: internalHeader}), 204)
	}
	ctx = dispatch(r, request{method: "GET", path: "/api/stats/abc1234", headers: internalHeader})
	if err := json.Decode(ctx.ResponseBody(), &st); err != nil || st.Clicks != 2 {
		t.Fatalf("stats = %+v %v", st, err)
	}
}

// ── gateway rate limiting over the real D1 adapter ─────────────────────────

func TestWasm_GatewayRateLimitOverD1(t *testing.T) {
	fake := newFakeD1()
	defer fake.release()
	installEnv(t, nil, map[string]js.Value{"DB": fake.binding()})
	db, err := d1.NewEdge("DB")
	if err != nil {
		t.Fatal(err)
	}
	g := newGateway()
	g.link.answers["POST /api/links"] = answer{status: 201, body: linkJSON}
	g.svc.Limiter = &gateway.D1Limiter{DB: db.RawConn(), Limit: gateway.DefaultLimit, WindowMs: gateway.DefaultWindowMs,
		Random: seqRandom(255), Log: g.log}
	r := edgeRouter(g.register)

	ip := map[string]string{shared.HeaderConnectingIP: "198.51.100.7"}
	for i := 0; i < gateway.DefaultLimit; i++ {
		wantStatus(t, dispatch(r, request{method: "POST", path: "/api/links", body: `{"url":"https://example.org"}`, headers: ip}), 201)
	}
	ctx := dispatch(r, request{method: "POST", path: "/api/links", body: `{"url":"https://example.org"}`, headers: ip})
	wantError(t, ctx, 429, shared.ErrCodeRateLimited)
	if ctx.GetHeader(shared.HeaderRetryAfter) != "10" {
		t.Fatalf("Retry-After = %q", ctx.GetHeader(shared.HeaderRetryAfter))
	}
	other := map[string]string{shared.HeaderConnectingIP: "198.51.100.8"}
	wantStatus(t, dispatch(r, request{method: "POST", path: "/api/links", body: `{"url":"https://example.org"}`, headers: other}), 201)
}

func TestWasm_GatewayRoutesOnEdgeRouter(t *testing.T) {
	g := newGateway()
	g.link.answers["GET /api/links/abc1234"] = answer{status: 200, body: linkJSON}
	g.clicks.answers["POST /api/clicks/abc1234"] = answer{status: 204}
	r := edgeRouter(g.register)

	page := dispatch(r, request{method: "GET", path: "/"})
	wantStatus(t, page, 200)
	if !strings.Contains(string(page.ResponseBody()), `id="shorten"`) {
		t.Fatal("GET / must serve the page on the edge router")
	}

	ctx := dispatch(r, request{method: "GET", path: "/abc1234"})
	wantStatus(t, ctx, 302)
	if ctx.GetHeader(shared.HeaderLocation) != "https://example.org/page" || ctx.GetHeader(shared.HeaderCacheControl) != "private, max-age=0" {
		t.Fatalf("headers %v", ctx.Headers())
	}
	// /api/links/{code}/stats must not be swallowed by /{code} or /api/links/{code}.
	g.link.answers["GET /api/links/abc1234/exists"] = answer{status: 200, body: `{"exists":false}`}
	wantError(t, dispatch(r, request{method: "GET", path: "/api/links/abc1234/stats"}), 404, shared.ErrCodeNotFound)
	if g.link.called("GET", "/api/links/abc1234/stats") {
		t.Fatal("stats route mismatched")
	}
}

// ── transport selection ─────────────────────────────────────────────────────

func TestWasm_FromEnvPrefersServiceBinding(t *testing.T) {
	var gotURL, gotSecret, gotMethod string
	fetchFn := js.FuncOf(func(_ js.Value, a []js.Value) any {
		gotURL = a[0].String()
		gotMethod = a[1].Get("method").String()
		gotSecret = a[1].Get("headers").Call("get", shared.HeaderInternal).String()
		return resolved(js.Global().Get("Response").New(`{"exists":true}`, map[string]any{"status": 200}))
	})
	defer fetchFn.Release()
	fetcher := js.Global().Get("Object").New()
	fetcher.Set("fetch", fetchFn)
	installEnv(t, map[string]string{"LINK_URL": "https://wisp-link.example.workers.dev"}, map[string]js.Value{"LINK": fetcher})

	c, err := transport.FromEnv("LINK", "LINK_URL", secret, transport.DefaultTimeoutMs)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.(*transport.BindingClient); !ok {
		t.Fatalf("a present binding must win, got %T", c)
	}
	var ex shared.Exists
	if _, err := transport.Call(c, "GET", "/api/links/abc1234/exists", nil, &ex); err != nil || !ex.Exists {
		t.Fatalf("call: %v %+v", err, ex)
	}
	if !strings.HasSuffix(gotURL, "/api/links/abc1234/exists") || gotSecret != secret || gotMethod != "GET" {
		t.Fatalf("binding saw %s %s %q", gotMethod, gotURL, gotSecret)
	}
}

func TestWasm_FromEnvFallsBackToURL(t *testing.T) {
	installEnv(t, map[string]string{"LINK_URL": "https://wisp-link.example.workers.dev"}, nil)
	c, err := transport.FromEnv("LINK", "LINK_URL", secret, transport.DefaultTimeoutMs)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.(*transport.URLClient); !ok {
		t.Fatalf("got %T, want URL mode", c)
	}

	installEnv(t, nil, nil)
	if _, err := transport.FromEnv("LINK", "LINK_URL", secret, transport.DefaultTimeoutMs); err == nil {
		t.Fatal("no binding and no URL must be a startup error")
	}
}

// TestWasm_URLClientCallsFetchWithoutThis guards the production crash where
// fetch was invoked as a method of js.Global(): inside a goflare Worker that
// object is a Proxy, and workerd throws "Illegal invocation" for any `this`
// other than undefined/globalThis. Invoking with an undefined `this` is the
// only form that works there, so the fake rejects anything else.
func TestWasm_URLClientCallsFetchWithoutThis(t *testing.T) {
	var gotThisOK, gotSignal bool
	var gotURL, gotSecret, gotMethod string
	fake := js.FuncOf(func(this js.Value, a []js.Value) any {
		gotThisOK = this.IsUndefined()
		gotURL = a[0].String()
		gotMethod = a[1].Get("method").String()
		gotSecret = a[1].Get("headers").Call("get", shared.HeaderInternal).String()
		gotSignal = a[1].Get("signal").Type() == js.TypeObject
		return resolved(js.Global().Get("Response").New(`{"code":"abc1234","clicks":3}`, map[string]any{"status": 200}))
	})
	defer fake.Release()
	orig := js.Global().Get("fetch")
	js.Global().Set("fetch", fake)
	defer js.Global().Set("fetch", orig)

	c := transport.NewURLClient("https://wisp-analytics.example.workers.dev/", secret, transport.BestEffortTimeoutMs)
	var st shared.Stats
	if _, err := transport.Call(c, "GET", "/api/stats/abc1234", nil, &st); err != nil || st.Clicks != 3 {
		t.Fatalf("call: %v %+v", err, st)
	}
	if !gotThisOK {
		t.Fatal("fetch was called with a `this`: workerd answers Illegal invocation")
	}
	if gotURL != "https://wisp-analytics.example.workers.dev/api/stats/abc1234" || gotMethod != "GET" || gotSecret != secret {
		t.Fatalf("fetch saw %s %s %q", gotMethod, gotURL, gotSecret)
	}
	if !gotSignal {
		t.Fatal("timeout must travel as an AbortSignal, not a setTimeout")
	}
}
