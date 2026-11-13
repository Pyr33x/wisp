package tests

import (
	"testing"

	"webtyp.com/json"
	"webtyp.com/router"
	"webtyp.com/storage"

	"github.com/pyr33x/wisp/gateway"
	"github.com/pyr33x/wisp/shared"
)

const (
	adminToken = "admin-token-value"
	linkJSON   = `{"code":"abc1234","short_url":"https://wisp.example.com/abc1234","origin":"https://example.org/page","created_at":5}`
	now        = int64(1_700_000_030_000) // 50 s into the window that starts at 1_699_999_980_000
)

type fakeLimiter struct {
	d    gateway.Decision
	err  error
	keys []string
}

func (l *fakeLimiter) Hit(key string, _ int64) (gateway.Decision, error) {
	l.keys = append(l.keys, key)
	return l.d, l.err
}

type gw struct {
	link, analytics, clicks *fakeClient
	limiter                 *fakeLimiter
	log                     *fakeLogger
	svc                     *gateway.Service
}

func newGateway() *gw {
	g := &gw{
		link:      newClient(map[string]answer{}),
		analytics: newClient(map[string]answer{}),
		clicks:    newClient(map[string]answer{}),
		limiter:   &fakeLimiter{d: gateway.Decision{Allowed: true}},
		log:       &fakeLogger{},
	}
	g.svc = &gateway.Service{
		Link: g.link, Analytics: g.analytics, Clicks: g.clicks, Limiter: g.limiter,
		BaseURL: "https://wisp.example.com", AdminToken: adminToken,
		Now: func() int64 { return now }, Log: g.log,
	}
	return g
}

func (g *gw) register(r router.Router) { g.svc.Register(r) }

func TestGateway_CreateCallsLinkAndReturns201(t *testing.T) {
	g := newGateway()
	g.link.answers["POST /api/links"] = answer{status: 201, body: linkJSON}
	ctx := serve(g.register, request{method: "POST", path: "/api/links", body: `{"url":"  https://example.org/page "}`,
		headers: map[string]string{shared.HeaderConnectingIP: "203.0.113.9"}})
	wantStatus(t, ctx, 201)
	var l shared.Link
	if err := json.Decode(ctx.ResponseBody(), &l); err != nil {
		t.Fatal(err)
	}
	if l.Code != "abc1234" || l.ShortURL != "https://wisp.example.com/abc1234" || l.Origin != "https://example.org/page" || l.CreatedAt != 5 {
		t.Fatalf("link = %+v", l)
	}
	if g.link.calls[0].body != `{"url":"https://example.org/page"}` {
		t.Fatalf("link must receive the trimmed URL, got %s", g.link.calls[0].body)
	}
	if len(g.limiter.keys) != 1 || g.limiter.keys[0] != "203.0.113.9" {
		t.Fatalf("rate limit key = %v", g.limiter.keys)
	}
}

func TestGateway_CreateValidates(t *testing.T) {
	for body, code := range map[string]string{
		`{"url":"https://wisp.example.com/abc1234"}`: shared.ErrCodeRedirectLoop,
		`{"url":"WISP.example.com"}`:                 shared.ErrCodeUnsupportedProto,
		`{"url":"https://"}`:                         shared.ErrCodeMissingHost,
		`{"url":""}`:                                 shared.ErrCodeInvalidURL,
		`[1,2`:                                       shared.ErrCodeInvalidJSON,
	} {
		g := newGateway()
		ctx := serve(g.register, request{method: "POST", path: "/api/links", body: body})
		wantError(t, ctx, 400, code)
		if len(g.link.calls) != 0 {
			t.Fatalf("%s: invalid input must not reach link", body)
		}
	}
}

func TestGateway_CreateRateLimited(t *testing.T) {
	g := newGateway()
	g.limiter.d = gateway.Decision{Allowed: false, RetryAfter: 30}
	ctx := serve(g.register, request{method: "POST", path: "/api/links", body: `{"url":"https://example.org"}`})
	wantError(t, ctx, 429, shared.ErrCodeRateLimited)
	if ctx.GetHeader(shared.HeaderRetryAfter) != "30" {
		t.Fatalf("Retry-After = %q", ctx.GetHeader(shared.HeaderRetryAfter))
	}
	if len(g.link.calls) != 0 {
		t.Fatal("a rate-limited request must not reach link")
	}
}

func TestGateway_CreateFailsOpenWhenLimiterStorageFails(t *testing.T) {
	g := newGateway()
	g.limiter.d, g.limiter.err = gateway.Decision{Allowed: true}, plainErr("D1 down")
	g.link.answers["POST /api/links"] = answer{status: 201, body: linkJSON}
	ctx := serve(g.register, request{method: "POST", path: "/api/links", body: `{"url":"https://example.org"}`})
	wantStatus(t, ctx, 201)
	if len(g.log.fails) != 1 || !contains(g.log.fails[0].detail, "D1 down") {
		t.Fatalf("limiter failure must be logged: %+v", g.log.fails)
	}
}

func TestGateway_CreateUpstreamFailureIs502WithoutDetails(t *testing.T) {
	for _, a := range []answer{{status: 500, body: `{"error":{"code":"internal_error","message":"D1 secret detail"}}`}, {err: plainErr("dial tcp: timeout")}} {
		g := newGateway()
		g.link.answers["POST /api/links"] = a
		ctx := serve(g.register, request{method: "POST", path: "/api/links", body: `{"url":"https://example.org"}`})
		wantError(t, ctx, 502, shared.ErrCodeUpstream)
		if contains(string(ctx.ResponseBody()), "secret") || contains(string(ctx.ResponseBody()), "dial") {
			t.Fatalf("internal detail leaked: %s", ctx.ResponseBody())
		}
		if len(g.log.fails) != 1 {
			t.Fatal("the cause must be logged")
		}
	}
}

func TestGateway_RedirectIs302AndCountsClick(t *testing.T) {
	g := newGateway()
	g.link.answers["GET /api/links/abc1234"] = answer{status: 200, body: linkJSON}
	g.clicks.answers["POST /api/clicks/abc1234"] = answer{status: 204}
	ctx := serve(g.register, request{method: "GET", path: "/abc1234"})
	wantStatus(t, ctx, 302)
	if ctx.GetHeader(shared.HeaderLocation) != "https://example.org/page" {
		t.Fatalf("Location = %q", ctx.GetHeader(shared.HeaderLocation))
	}
	if ctx.GetHeader(shared.HeaderCacheControl) != "private, max-age=0" {
		t.Fatalf("Cache-Control = %q", ctx.GetHeader(shared.HeaderCacheControl))
	}
	if !g.clicks.called("POST", "/api/clicks/abc1234") {
		t.Fatal("click not recorded")
	}
	if len(g.analytics.calls) != 0 {
		t.Fatal("clicks must use the best-effort client")
	}
}

func TestGateway_RedirectSurvivesAnalyticsFailure(t *testing.T) {
	for _, a := range []answer{{err: plainErr("timeout")}, {status: 500}} {
		g := newGateway()
		g.link.answers["GET /api/links/abc1234"] = answer{status: 200, body: linkJSON}
		g.clicks.answers["POST /api/clicks/abc1234"] = a
		ctx := serve(g.register, request{method: "GET", path: "/abc1234"})
		wantStatus(t, ctx, 302)
		if len(g.log.fails) != 1 {
			t.Fatalf("lost click must be logged: %+v", g.log.fails)
		}
	}
}

func TestGateway_RedirectUnknownDeletedOrMalformedIs404(t *testing.T) {
	g := newGateway()
	g.link.answers["GET /api/links/gone123"] = answer{status: 404, body: `{"error":{"code":"not_found","message":"x"}}`}
	wantError(t, serve(g.register, request{method: "GET", path: "/gone123"}), 404, shared.ErrCodeNotFound)
	wantError(t, serve(g.register, request{method: "GET", path: "/favicon.ico"}), 404, shared.ErrCodeNotFound)
	if len(g.clicks.calls) != 0 {
		t.Fatal("no click for a missing link")
	}
	if len(g.link.calls) != 1 {
		t.Fatal("a malformed code must not reach link")
	}
}

func TestGateway_RedirectLinkDownIs502(t *testing.T) {
	g := newGateway()
	g.link.answers["GET /api/links/abc1234"] = answer{err: plainErr("network")}
	wantError(t, serve(g.register, request{method: "GET", path: "/abc1234"}), 502, shared.ErrCodeUpstream)
}

func TestGateway_StatsChecksLinkThenAnalytics(t *testing.T) {
	g := newGateway()
	g.link.answers["GET /api/links/abc1234/exists"] = answer{status: 200, body: `{"exists":true}`}
	g.analytics.answers["GET /api/stats/abc1234"] = answer{status: 200, body: `{"code":"abc1234","clicks":42}`}
	ctx := serve(g.register, request{method: "GET", path: "/api/links/abc1234/stats"})
	wantStatus(t, ctx, 200)
	var st shared.Stats
	if err := json.Decode(ctx.ResponseBody(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Code != "abc1234" || st.Clicks != 42 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestGateway_StatsForDeletedLinkIs404(t *testing.T) {
	g := newGateway()
	g.link.answers["GET /api/links/abc1234/exists"] = answer{status: 200, body: `{"exists":false}`}
	wantError(t, serve(g.register, request{method: "GET", path: "/api/links/abc1234/stats"}), 404, shared.ErrCodeNotFound)
	if len(g.analytics.calls) != 0 {
		t.Fatal("analytics must not be asked about a deleted link")
	}
	wantError(t, serve(g.register, request{method: "GET", path: "/api/links/b-d/stats"}), 404, shared.ErrCodeNotFound)
}

func TestGateway_StatsUpstreamFailureIs502(t *testing.T) {
	g := newGateway()
	g.link.answers["GET /api/links/abc1234/exists"] = answer{status: 200, body: `{"exists":true}`}
	g.analytics.answers["GET /api/stats/abc1234"] = answer{status: 503}
	wantError(t, serve(g.register, request{method: "GET", path: "/api/links/abc1234/stats"}), 502, shared.ErrCodeUpstream)
}

func TestGateway_DeleteRequiresAdminToken(t *testing.T) {
	for _, h := range []map[string]string{
		nil,
		{shared.HeaderAuthorization: adminToken},
		{shared.HeaderAuthorization: "Bearer wrong"},
		{shared.HeaderAuthorization: "Basic " + adminToken},
		{shared.HeaderAuthorization: "Bearer " + adminToken + "x"},
	} {
		g := newGateway()
		wantError(t, serve(g.register, request{method: "DELETE", path: "/api/links/abc1234", headers: h}), 401, shared.ErrCodeUnauthorized)
		if len(g.link.calls) != 0 {
			t.Fatal("unauthorized delete reached link")
		}
	}
}

func TestGateway_DeleteWithEmptyConfiguredTokenDenies(t *testing.T) {
	g := newGateway()
	g.svc.AdminToken = ""
	wantError(t, serve(g.register, request{method: "DELETE", path: "/api/links/abc1234",
		headers: map[string]string{shared.HeaderAuthorization: "Bearer "}}), 401, shared.ErrCodeUnauthorized)
}

func TestGateway_DeleteSoftDeletesThroughLink(t *testing.T) {
	g := newGateway()
	g.link.answers["DELETE /api/links/abc1234"] = answer{status: 204}
	ctx := serve(g.register, request{method: "DELETE", path: "/api/links/abc1234",
		headers: map[string]string{shared.HeaderAuthorization: "Bearer " + adminToken}})
	wantStatus(t, ctx, 204)
	if !g.link.called("DELETE", "/api/links/abc1234") {
		t.Fatal("link not called")
	}
	g.link.answers["DELETE /api/links/abc1234"] = answer{status: 500}
	wantError(t, serve(g.register, request{method: "DELETE", path: "/api/links/abc1234",
		headers: map[string]string{shared.HeaderAuthorization: "Bearer " + adminToken}}), 502, shared.ErrCodeUpstream)
}

// ── D1Limiter ───────────────────────────────────────────────────────────────

func limiterWithCount(count int64, rnd shared.RandomSource) (*gateway.D1Limiter, *fakeExec) {
	db := &fakeExec{onQuery: func(string, []any) scanFunc {
		return func(dest ...any) error { *dest[0].(*int64) = count; return nil }
	}}
	return &gateway.D1Limiter{DB: db, Limit: 10, WindowMs: 60_000, Random: rnd, Log: &fakeLogger{}}, db
}

func TestD1Limiter_AllowsUpToLimit(t *testing.T) {
	l, db := limiterWithCount(10, seqRandom(255))
	d, err := l.Hit("1.2.3.4", now)
	if err != nil || !d.Allowed {
		t.Fatalf("10th hit must pass: %+v %v", d, err)
	}
	q := db.queries[0]
	if !contains(q.query, "ON CONFLICT(ip, window_start) DO UPDATE SET count = count + 1 RETURNING count") {
		t.Fatalf("hit is not one atomic upsert: %s", q.query)
	}
	if q.args[0] != "1.2.3.4" || q.args[1] != int64(1_699_999_980_000) {
		t.Fatalf("args = %v (window must be aligned to the minute)", q.args)
	}
	if len(db.execs) != 0 {
		t.Fatal("cleanup ran although the random byte was above the threshold")
	}
}

func TestD1Limiter_BlocksOverLimitWithRetryAfter(t *testing.T) {
	l, _ := limiterWithCount(11, seqRandom(255))
	d, err := l.Hit("1.2.3.4", now)
	if err != nil || d.Allowed || d.RetryAfter != 10 {
		t.Fatalf("11th hit: %+v %v (want blocked, retry 10 s)", d, err)
	}
	d, _ = l.Hit("1.2.3.4", 1_700_000_039_999)
	if d.RetryAfter != 1 {
		t.Fatalf("1 ms before reset Retry-After must be 1, got %d", d.RetryAfter)
	}
}

func TestD1Limiter_StorageErrorAllows(t *testing.T) {
	db := &fakeExec{onQuery: func(string, []any) scanFunc {
		return func(...any) error { return plainErr("D1 down") }
	}}
	l := &gateway.D1Limiter{DB: db, Limit: 10, WindowMs: 60_000, Random: seqRandom(0), Log: &fakeLogger{}}
	d, err := l.Hit("1.2.3.4", now)
	if err == nil || !d.Allowed {
		t.Fatalf("must fail open and report: %+v %v", d, err)
	}
}

func TestD1Limiter_OpportunisticCleanup(t *testing.T) {
	l, db := limiterWithCount(1, seqRandom(0))
	if _, err := l.Hit("1.2.3.4", now); err != nil {
		t.Fatal(err)
	}
	if len(db.execs) != 1 || !contains(db.execs[0].query, "DELETE FROM rate_limits WHERE window_start < ?") ||
		db.execs[0].args[0] != int64(1_699_999_980_000-60_000) {
		t.Fatalf("cleanup = %+v", db.execs)
	}

	// A failing cleanup is logged and never changes the decision.
	log := &fakeLogger{}
	l.Log = log
	db.onExec = func(string, []any) error { return plainErr("busy") }
	d, err := l.Hit("1.2.3.4", now)
	if err != nil || !d.Allowed || len(log.fails) != 1 {
		t.Fatalf("decision %+v err %v fails %+v", d, err, log.fails)
	}
}

var _ storage.Executor = (*fakeExec)(nil)
