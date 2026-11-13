// Package link is the link service: it owns the links table in the wisp-links
// D1 database and is the only place short URLs are built.
package link

import (
	"webtyp.com/storage"
)

// Schema is the links table. cmd/migrate applies it from CI; no Worker runs DDL.
const Schema = `CREATE TABLE IF NOT EXISTS links (
	id INTEGER PRIMARY KEY,
	code TEXT NOT NULL UNIQUE,
	origin TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	deleted_at INTEGER NULL
)`

const (
	sqlInsert     = `INSERT INTO links (code, origin, created_at) VALUES (?, ?, ?)`
	sqlActive     = `SELECT origin, created_at FROM links WHERE code = ? AND deleted_at IS NULL`
	sqlExists     = `SELECT 1 FROM links WHERE code = ? AND deleted_at IS NULL`
	sqlSoftDelete = `UPDATE links SET deleted_at = ? WHERE code = ? AND deleted_at IS NULL`
)

// Row is an active link as stored.
type Row struct {
	Origin    string
	CreatedAt int64
}

// Store is the links table. Insert returns an error that satisfies
// storage.IsDuplicate when the code already exists; Active and Exists only see
// rows whose deleted_at is NULL.
type Store interface {
	Insert(code, origin string, createdAt int64) error
	Active(code string) (row Row, found bool, err error)
	Exists(code string) (bool, error)
	SoftDelete(code string, deletedAt int64) error
}

// SQLStore is Store over any storage.Executor: d1.NewEdge(...).RawConn() in
// the Worker, a fake in tests.
type SQLStore struct {
	DB storage.Executor
}

func (s *SQLStore) Insert(code, origin string, createdAt int64) error {
	_, err := s.DB.Exec(sqlInsert, code, origin, createdAt)
	return err
}

func (s *SQLStore) Active(code string) (Row, bool, error) {
	var r Row
	err := s.DB.QueryRow(sqlActive, code).Scan(&r.Origin, &r.CreatedAt)
	if storage.IsNoRows(err) {
		return Row{}, false, nil
	}
	if err != nil {
		return Row{}, false, err
	}
	return r, true, nil
}

func (s *SQLStore) Exists(code string) (bool, error) {
	var one int64
	err := s.DB.QueryRow(sqlExists, code).Scan(&one)
	if storage.IsNoRows(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *SQLStore) SoftDelete(code string, deletedAt int64) error {
	// Matching only active rows makes a repeated delete a no-op that keeps the
	// first deleted_at: idempotent without a read.
	_, err := s.DB.Exec(sqlSoftDelete, deletedAt, code)
	return err
}
