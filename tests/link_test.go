package tests

import (
	"testing"

	"webtyp.com/json"
	"webtyp.com/router"
	"webtyp.com/storage"

	"github.com/pyr33x/wisp/link"
	"github.com/pyr33x/wisp/shared"
)

const secret = "internal-secret-value"

var internalHeader = map[string]string{shared.HeaderInternal: secret}

// memLinks is an in-memory link.Store with the same semantics as the table.
type memLinks struct {
	rows      map[string]*memLink
	insertErr error
}

type memLink struct {
	origin    string
	createdAt int64
	deletedAt int64
}

func newMemLinks() *memLinks { return &memLinks{rows: map[string]*memLink{}} }

func (m *memLinks) Insert(code, origin string, createdAt int64) error {
	if m.insertErr != nil {
		return m.insertErr
	}
	if _, ok := m.rows[code]; ok {
		return storage.Duplicate(errUnique)
	}
	m.rows[code] = &memLink{origin: origin, createdAt: createdAt}
	return nil
}

func (m *memLinks) Active(code string) (link.Row, bool, error) {
	r, ok := m.rows[code]
	if !ok || r.deletedAt != 0 {
		return link.Row{}, false, nil
	}
	return link.Row{Origin: r.origin, CreatedAt: r.createdAt}, true, nil
}

func (m *memLinks) Exists(code string) (bool, error) {
	_, ok, err := m.Active(code)
	return ok, err
}

func (m *memLinks) SoftDelete(code string, at int64) error {
	if r, ok := m.rows[code]; ok && r.deletedAt == 0 {
		r.deletedAt = at
	}
	return nil
}

type plainErr string

func (e plainErr) Error() string { return string(e) }

const errUnique = plainErr("D1_ERROR: UNIQUE constraint failed: links.code")

func linkService(store link.Store, rnd shared.RandomSource, log *fakeLogger) func(router.Router) {
	s := &link.Service{
		Store:   store,
		BaseURL: "https://wisp.example.com/",
		Random:  rnd,
		Now:     func() int64 { return 1_700_000_000_000 },
		Log:     log,
		Secret:  secret,
	}
	return s.Register
}

func TestLink_RejectsMissingOrWrongInternalSecret(t *testing.T) {
	for _, h := range []map[string]string{nil, {shared.HeaderInternal: "wrong"}, {shared.HeaderInternal: ""}} {
		log := &fakeLogger{}
		ctx := serve(linkService(newMemLinks(), cryptoSource, log), request{method: "GET", path: "/api/links/abc1234", headers: h})
		wantError(t, ctx, 401, shared.ErrCodeUnauthorized)
		if len(log.rejs) != 1 {
			t.Fatalf("a refused internal call must be logged once, got %d", len(log.rejs))
		}
	}
}

func TestLink_EmptySecretDeniesEverything(t *testing.T) {
	s := &link.Service{Store: newMemLinks(), BaseURL: "https://w.io", Random: cryptoSource,
		Now: func() int64 { return 1 }, Log: &fakeLogger{}, Secret: ""}
	ctx := serve(s.Register, request{method: "GET", path: "/api/links/abc1234", headers: map[string]string{shared.HeaderInternal: ""}})
	wantError(t, ctx, 401, shared.ErrCodeUnauthorized)
}

func TestLink_CreateReturnsShortURL(t *testing.T) {
	store := newMemLinks()
	ctx := serve(linkService(store, seqRandom(1, 2, 3, 4, 5, 6, 7), &fakeLogger{}), request{
		method: "POST", path: "/api/links", body: `{"url":" https://example.org/a "}`, headers: internalHeader,
	})
	wantStatus(t, ctx, 201)
	var l shared.Link
	if err := json.Decode(ctx.ResponseBody(), &l); err != nil {
		t.Fatal(err)
	}
	if l.Code != "1234567" || l.ShortURL != "https://wisp.example.com/1234567" ||
		l.Origin != "https://example.org/a" || l.CreatedAt != 1_700_000_000_000 {
		t.Fatalf("unexpected link %+v", l)
	}
	if _, ok := store.rows["1234567"]; !ok {
		t.Fatal("link not stored")
	}
}

func TestLink_CreateRetriesOnCollision(t *testing.T) {
	store := newMemLinks()
	store.rows["1111111"] = &memLink{origin: "https://taken.io"}
	// First code collides, second ("2222222") is free.
	rnd := seqRandom(1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2)
	ctx := serve(linkService(store, rnd, &fakeLogger{}), request{
		method: "POST", path: "/api/links", body: `{"url":"https://example.org"}`, headers: internalHeader,
	})
	wantStatus(t, ctx, 201)
	if store.rows["2222222"] == nil || store.rows["1111111"].origin != "https://taken.io" {
		t.Fatal("collision must be retried with a new code and never overwrite")
	}
}

func TestLink_CreateGivesUpAfterFiveCollisions(t *testing.T) {
	store := newMemLinks()
	store.rows["1111111"] = &memLink{origin: "https://taken.io"}
	log := &fakeLogger{}
	ctx := serve(linkService(store, seqRandom(1), log), request{
		method: "POST", path: "/api/links", body: `{"url":"https://example.org"}`, headers: internalHeader,
	})
	wantError(t, ctx, 500, shared.ErrCodeInternal)
	if len(log.fails) != 1 {
		t.Fatalf("exhaustion must be logged, got %v", log.fails)
	}
}

func TestLink_CreateCountsExactlyFiveAttempts(t *testing.T) {
	db := &fakeExec{onExec: func(string, []any) error { return storage.Duplicate(errUnique) }}
	ctx := serve(linkService(&link.SQLStore{DB: db}, cryptoSource, &fakeLogger{}), request{
		method: "POST", path: "/api/links", body: `{"url":"https://example.org"}`, headers: internalHeader,
	})
	wantStatus(t, ctx, 500)
	if len(db.execs) != shared.MaxCodeAttempts {
		t.Fatalf("attempts = %d, want %d", len(db.execs), shared.MaxCodeAttempts)
	}
	if len(db.queries) != 0 {
		t.Fatal("create must never check before inserting")
	}
}

func TestLink_CreateStorageErrorIs500(t *testing.T) {
	store := newMemLinks()
	store.insertErr = plainErr("D1 down")
	log := &fakeLogger{}
	ctx := serve(linkService(store, cryptoSource, log), request{
		method: "POST", path: "/api/links", body: `{"url":"https://example.org"}`, headers: internalHeader,
	})
	wantError(t, ctx, 500, shared.ErrCodeInternal)
	if len(log.fails) != 1 || log.fails[0].detail != "D1 down" {
		t.Fatalf("cause must be logged: %+v", log.fails)
	}
}

func TestLink_CreateRejectsBadInput(t *testing.T) {
	for body, code := range map[string]string{
		`not json`:             shared.ErrCodeInvalidJSON,
		`{"url":"ftp://x.org"}`: shared.ErrCodeUnsupportedProto,
		`{}`:                   shared.ErrCodeInvalidURL,
	} {
		ctx := serve(linkService(newMemLinks(), cryptoSource, &fakeLogger{}), request{
			method: "POST", path: "/api/links", body: body, headers: internalHeader,
		})
		wantError(t, ctx, 400, code)
	}
}

func TestLink_ResolveActiveDeletedUnknown(t *testing.T) {
	store := newMemLinks()
	store.rows["abc1234"] = &memLink{origin: "https://example.org", createdAt: 5}
	store.rows["del1234"] = &memLink{origin: "https://gone.org", createdAt: 5, deletedAt: 9}
	reg := linkService(store, cryptoSource, &fakeLogger{})

	ctx := serve(reg, request{method: "GET", path: "/api/links/abc1234", headers: internalHeader})
	wantStatus(t, ctx, 200)
	var l shared.Link
	if err := json.Decode(ctx.ResponseBody(), &l); err != nil {
		t.Fatal(err)
	}
	if l.Origin != "https://example.org" || l.ShortURL != "https://wisp.example.com/abc1234" || l.CreatedAt != 5 {
		t.Fatalf("unexpected %+v", l)
	}

	for _, p := range []string{"/api/links/del1234", "/api/links/nope123", "/api/links/bad"} {
		wantError(t, serve(reg, request{method: "GET", path: p, headers: internalHeader}), 404, shared.ErrCodeNotFound)
	}
}

func TestLink_Exists(t *testing.T) {
	store := newMemLinks()
	store.rows["abc1234"] = &memLink{origin: "https://example.org"}
	store.rows["del1234"] = &memLink{origin: "https://gone.org", deletedAt: 9}
	reg := linkService(store, cryptoSource, &fakeLogger{})
	for path, want := range map[string]bool{
		"/api/links/abc1234/exists": true,
		"/api/links/del1234/exists": false,
		"/api/links/nope123/exists": false,
		"/api/links/x/exists":       false,
	} {
		ctx := serve(reg, request{method: "GET", path: path, headers: internalHeader})
		wantStatus(t, ctx, 200)
		var e shared.Exists
		if err := json.Decode(ctx.ResponseBody(), &e); err != nil {
			t.Fatal(err)
		}
		if e.Exists != want {
			t.Errorf("%s: exists = %v, want %v", path, e.Exists, want)
		}
	}
}

func TestLink_DeleteIsIdempotentSoftDelete(t *testing.T) {
	store := newMemLinks()
	store.rows["abc1234"] = &memLink{origin: "https://example.org"}
	reg := linkService(store, cryptoSource, &fakeLogger{})
	for i := 0; i < 2; i++ {
		wantStatus(t, serve(reg, request{method: "DELETE", path: "/api/links/abc1234", headers: internalHeader}), 204)
	}
	if store.rows["abc1234"].deletedAt != 1_700_000_000_000 {
		t.Fatal("row must be kept with deleted_at set")
	}
	wantStatus(t, serve(reg, request{method: "DELETE", path: "/api/links/nope123", headers: internalHeader}), 204)
	wantError(t, serve(reg, request{method: "GET", path: "/api/links/abc1234", headers: internalHeader}), 404, shared.ErrCodeNotFound)
}

func TestLinkSQLStore_Statements(t *testing.T) {
	db := &fakeExec{}
	s := &link.SQLStore{DB: db}

	if _, found, err := s.Active("abc1234"); found || err != nil {
		t.Fatalf("no rows must be not-found, got %v %v", found, err)
	}
	if ok, err := s.Exists("abc1234"); ok || err != nil {
		t.Fatalf("no rows must be false, got %v %v", ok, err)
	}
	for _, q := range db.queries {
		if !contains(q.query, "deleted_at IS NULL") {
			t.Errorf("read ignores soft delete: %s", q.query)
		}
	}
	if err := s.SoftDelete("abc1234", 42); err != nil {
		t.Fatal(err)
	}
	last := db.execs[len(db.execs)-1]
	if !contains(last.query, "UPDATE links SET deleted_at") || last.args[0] != int64(42) || last.args[1] != "abc1234" {
		t.Fatalf("soft delete = %s %v", last.query, last.args)
	}

	db.onQuery = func(string, []any) scanFunc {
		return func(dest ...any) error {
			*dest[0].(*string) = "https://example.org"
			*dest[1].(*int64) = 7
			return nil
		}
	}
	row, found, err := s.Active("abc1234")
	if err != nil || !found || row.Origin != "https://example.org" || row.CreatedAt != 7 {
		t.Fatalf("active = %+v %v %v", row, found, err)
	}

	db.onExec = func(string, []any) error { return storage.Duplicate(errUnique) }
	if err := s.Insert("abc1234", "https://x.io", 1); !storage.IsDuplicate(err) {
		t.Fatalf("duplicate must surface as storage.IsDuplicate, got %v", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
