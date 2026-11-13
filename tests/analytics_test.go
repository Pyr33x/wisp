package tests

import (
	"testing"

	"webtyp.com/json"
	"webtyp.com/router"

	"github.com/pyr33x/wisp/analytics"
	"github.com/pyr33x/wisp/shared"
)

type memClicks struct {
	n   map[string]int64
	err error
}

func (m *memClicks) Increment(code string) error {
	if m.err != nil {
		return m.err
	}
	m.n[code]++
	return nil
}

func (m *memClicks) Clicks(code string) (int64, error) {
	if m.err != nil {
		return 0, m.err
	}
	return m.n[code], nil
}

func analyticsService(store analytics.Store, log *fakeLogger) func(router.Router) {
	s := &analytics.Service{Store: store, Log: log, Secret: secret}
	return s.Register
}

func TestAnalytics_RequiresInternalSecret(t *testing.T) {
	reg := analyticsService(&memClicks{n: map[string]int64{}}, &fakeLogger{})
	wantError(t, serve(reg, request{method: "POST", path: "/api/clicks/abc1234"}), 401, shared.ErrCodeUnauthorized)
	wantError(t, serve(reg, request{method: "GET", path: "/api/stats/abc1234",
		headers: map[string]string{shared.HeaderInternal: secret + "x"}}), 401, shared.ErrCodeUnauthorized)
}

func TestAnalytics_ClickThenStats(t *testing.T) {
	store := &memClicks{n: map[string]int64{}}
	reg := analyticsService(store, &fakeLogger{})
	for i := 0; i < 3; i++ {
		wantStatus(t, serve(reg, request{method: "POST", path: "/api/clicks/abc1234", headers: internalHeader}), 204)
	}
	ctx := serve(reg, request{method: "GET", path: "/api/stats/abc1234", headers: internalHeader})
	wantStatus(t, ctx, 200)
	var st shared.Stats
	if err := json.Decode(ctx.ResponseBody(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Code != "abc1234" || st.Clicks != 3 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestAnalytics_StatsWithoutRowIsZero(t *testing.T) {
	ctx := serve(analyticsService(&analytics.SQLStore{DB: &fakeExec{}}, &fakeLogger{}),
		request{method: "GET", path: "/api/stats/abc1234", headers: internalHeader})
	wantStatus(t, ctx, 200)
	var st shared.Stats
	if err := json.Decode(ctx.ResponseBody(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Clicks != 0 {
		t.Fatalf("clicks = %d", st.Clicks)
	}
}

func TestAnalytics_InvalidCodeIs400(t *testing.T) {
	reg := analyticsService(&memClicks{n: map[string]int64{}}, &fakeLogger{})
	wantError(t, serve(reg, request{method: "POST", path: "/api/clicks/bad", headers: internalHeader}), 400, shared.ErrCodeInvalidCode)
	wantError(t, serve(reg, request{method: "GET", path: "/api/stats/bad!bad", headers: internalHeader}), 400, shared.ErrCodeInvalidCode)
}

func TestAnalytics_StorageErrorIs500AndLogged(t *testing.T) {
	log := &fakeLogger{}
	reg := analyticsService(&memClicks{n: map[string]int64{}, err: plainErr("D1 down")}, log)
	wantError(t, serve(reg, request{method: "POST", path: "/api/clicks/abc1234", headers: internalHeader}), 500, shared.ErrCodeInternal)
	wantError(t, serve(reg, request{method: "GET", path: "/api/stats/abc1234", headers: internalHeader}), 500, shared.ErrCodeInternal)
	if len(log.fails) != 2 {
		t.Fatalf("fails = %+v", log.fails)
	}
}

func TestAnalyticsSQLStore_IncrementIsOneAtomicUpsert(t *testing.T) {
	db := &fakeExec{}
	s := &analytics.SQLStore{DB: db}
	if err := s.Increment("abc1234"); err != nil {
		t.Fatal(err)
	}
	if len(db.execs) != 1 || len(db.queries) != 0 {
		t.Fatalf("increment must be one statement and no read: execs=%d queries=%d", len(db.execs), len(db.queries))
	}
	q := db.execs[0].query
	if !contains(q, "ON CONFLICT(code) DO UPDATE SET clicks = clicks + 1") || db.execs[0].args[0] != "abc1234" {
		t.Fatalf("increment = %s %v", q, db.execs[0].args)
	}
}
