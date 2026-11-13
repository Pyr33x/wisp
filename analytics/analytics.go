// Package analytics is the analytics service: it owns the analytics table in
// the wisp-analytics D1 database. It never checks whether a link exists;
// gateway does that against link before asking for stats.
package analytics

import (
	"webtyp.com/router"
	"webtyp.com/storage"

	"github.com/pyr33x/wisp/shared"
	"github.com/pyr33x/wisp/transport"
)

// Schema is the analytics table. cmd/migrate applies it from CI.
const Schema = `CREATE TABLE IF NOT EXISTS analytics (
	id INTEGER PRIMARY KEY,
	code TEXT NOT NULL UNIQUE,
	clicks INTEGER NOT NULL DEFAULT 0
)`

const (
	// One atomic statement: no read-then-write, so concurrent clicks never lose a count.
	sqlIncrement = `INSERT INTO analytics (code, clicks) VALUES (?, 1) ON CONFLICT(code) DO UPDATE SET clicks = clicks + 1`
	sqlClicks    = `SELECT clicks FROM analytics WHERE code = ?`
)

// Store is the analytics table.
type Store interface {
	Increment(code string) error
	// Clicks returns 0 when the code has never been clicked.
	Clicks(code string) (int64, error)
}

// SQLStore is Store over any storage.Executor.
type SQLStore struct {
	DB storage.Executor
}

func (s *SQLStore) Increment(code string) error {
	_, err := s.DB.Exec(sqlIncrement, code)
	return err
}

func (s *SQLStore) Clicks(code string) (int64, error) {
	var n int64
	err := s.DB.QueryRow(sqlClicks, code).Scan(&n)
	if storage.IsNoRows(err) {
		return 0, nil
	}
	return n, err
}

// Service holds analytics' dependencies. Every field is required.
type Service struct {
	Store  Store
	Log    shared.Logger
	Secret string // INTERNAL_SECRET
}

// Register mounts analytics' routes on r, all behind the internal-secret check.
func (s *Service) Register(r router.Router) {
	r.Use(transport.RequireInternal(s.Secret, s.Log))
	r.Post(shared.PathClick, s.click).Public()
	r.Get(shared.PathStats, s.stats).Public()
}

func (s *Service) click(ctx router.Context) {
	code := ctx.Param(shared.ParamCode)
	if !shared.ValidCode(code) {
		shared.WriteError(ctx, 400, shared.ErrCodeInvalidCode, shared.MsgInvalidCode)
		return
	}
	if err := s.Store.Increment(code); err != nil {
		s.Log.Fail(500, ctx.Method(), ctx.Path(), err)
		shared.WriteError(ctx, 500, shared.ErrCodeInternal, shared.MsgInternal)
		return
	}
	shared.WriteNoContent(ctx)
}

func (s *Service) stats(ctx router.Context) {
	code := ctx.Param(shared.ParamCode)
	if !shared.ValidCode(code) {
		shared.WriteError(ctx, 400, shared.ErrCodeInvalidCode, shared.MsgInvalidCode)
		return
	}
	n, err := s.Store.Clicks(code)
	if err != nil {
		s.Log.Fail(500, ctx.Method(), ctx.Path(), err)
		shared.WriteError(ctx, 500, shared.ErrCodeInternal, shared.MsgInternal)
		return
	}
	shared.WriteJSON(ctx, 200, &shared.Stats{Code: code, Clicks: n})
}
