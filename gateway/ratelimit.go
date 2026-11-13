package gateway

import (
	"webtyp.com/storage"

	"github.com/pyr33x/wisp/shared"
)

// RateLimitSchema is the fixed-window counter table in the wisp-gateway D1
// database. The Workers Rate Limiting binding is not used because goflare
// cannot declare it; this table costs one D1 write per create.
const RateLimitSchema = `CREATE TABLE IF NOT EXISTS rate_limits (
	ip TEXT NOT NULL,
	window_start INTEGER NOT NULL,
	count INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (ip, window_start)
) WITHOUT ROWID`

// RateLimitIndex makes the opportunistic cleanup a range scan.
const RateLimitIndex = `CREATE INDEX IF NOT EXISTS rate_limits_window ON rate_limits (window_start)`

const (
	// Atomic increment-and-read: concurrent creates from one IP can never
	// both see the same count.
	sqlHit = `INSERT INTO rate_limits (ip, window_start, count) VALUES (?, ?, 1) ` +
		`ON CONFLICT(ip, window_start) DO UPDATE SET count = count + 1 RETURNING count`
	sqlCleanup = `DELETE FROM rate_limits WHERE window_start < ?`
)

// Defaults for POST /api/links: 10 creates per minute per IP.
const (
	DefaultLimit    = 10
	DefaultWindowMs = 60_000
	// cleanupThreshold out of 256: a random byte below it triggers cleanup,
	// so roughly 1 request in 50 deletes expired windows.
	cleanupThreshold = 5
	msPerSecond      = 1000
)

// Decision is the outcome of one rate-limit check.
type Decision struct {
	Allowed bool
	// RetryAfter is whole seconds until the window resets; set when !Allowed.
	RetryAfter int
}

// Limiter counts one hit for key at nowMs. An error means the count could not
// be stored; the caller fails open.
type Limiter interface {
	Hit(key string, nowMs int64) (Decision, error)
}

// D1Limiter is a fixed-window counter over any storage.Executor.
type D1Limiter struct {
	DB       storage.Executor
	Limit    int64
	WindowMs int64
	Random   shared.RandomSource
	Log      shared.Logger
}

func (l *D1Limiter) Hit(key string, nowMs int64) (Decision, error) {
	window := nowMs - nowMs%l.WindowMs
	var count int64
	if err := l.DB.QueryRow(sqlHit, key, window).Scan(&count); err != nil {
		return Decision{Allowed: true}, err
	}
	l.maybeCleanup(window)
	if count <= l.Limit {
		return Decision{Allowed: true}, nil
	}
	remaining := window + l.WindowMs - nowMs
	retry := int((remaining + msPerSecond - 1) / msPerSecond)
	if retry < 1 {
		retry = 1
	}
	return Decision{Allowed: false, RetryAfter: retry}, nil
}

// maybeCleanup deletes windows older than the previous one, on a random
// fraction of requests. Its failure costs nothing but table size, so it is
// logged and ignored.
func (l *D1Limiter) maybeCleanup(window int64) {
	var b [1]byte
	if err := l.Random(b[:]); err != nil || b[0] >= cleanupThreshold {
		return
	}
	if _, err := l.DB.Exec(sqlCleanup, window-l.WindowMs); err != nil {
		l.Log.Fail(200, "CLEANUP", "rate_limits", err)
	}
}
