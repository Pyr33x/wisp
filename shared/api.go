// Package shared holds the HTTP contracts every wisp Worker speaks: request and
// response bodies, error codes, header names and the validation rules that more
// than one service applies. It carries no build tag, so it compiles into each
// Worker's wasm and into native tests alike, and it imports no standard library
// package that would bloat a TinyGo binary.
//
// The JSON codec is webtyp/json, which uses no reflection: every body type
// implements model.Encodable / model.Decodable by hand below.
package shared

import "webtyp.com/model"

// CreateLinkRequest is the body of POST /api/links on gateway and on link.
type CreateLinkRequest struct {
	URL string
}

func (m *CreateLinkRequest) IsNil() bool { return m == nil }

func (m *CreateLinkRequest) EncodeFields(w model.FieldWriter) {
	w.String("url", m.URL)
}

func (m *CreateLinkRequest) DecodeFields(r model.FieldReader) {
	if v, ok := r.String("url"); ok {
		m.URL = v
	}
}

// Link is a short link as link returns it and as gateway hands it to clients.
// CreatedAt is unix milliseconds.
type Link struct {
	Code      string
	ShortURL  string
	Origin    string
	CreatedAt int64
}

func (m *Link) IsNil() bool { return m == nil }

func (m *Link) EncodeFields(w model.FieldWriter) {
	w.String("code", m.Code)
	w.String("short_url", m.ShortURL)
	w.String("origin", m.Origin)
	w.Int("created_at", m.CreatedAt)
}

func (m *Link) DecodeFields(r model.FieldReader) {
	if v, ok := r.String("code"); ok {
		m.Code = v
	}
	if v, ok := r.String("short_url"); ok {
		m.ShortURL = v
	}
	if v, ok := r.String("origin"); ok {
		m.Origin = v
	}
	if v, ok := r.Int("created_at"); ok {
		m.CreatedAt = v
	}
}

// Exists is the body of link's GET /api/links/{code}/exists.
type Exists struct {
	Exists bool
}

func (m *Exists) IsNil() bool { return m == nil }

func (m *Exists) EncodeFields(w model.FieldWriter) {
	w.Bool("exists", m.Exists)
}

func (m *Exists) DecodeFields(r model.FieldReader) {
	if v, ok := r.Bool("exists"); ok {
		m.Exists = v
	}
}

// Stats is the body of analytics' GET /api/stats/{code} and of gateway's
// GET /api/links/{code}/stats.
type Stats struct {
	Code   string
	Clicks int64
}

func (m *Stats) IsNil() bool { return m == nil }

func (m *Stats) EncodeFields(w model.FieldWriter) {
	w.String("code", m.Code)
	w.Int("clicks", m.Clicks)
}

func (m *Stats) DecodeFields(r model.FieldReader) {
	if v, ok := r.String("code"); ok {
		m.Code = v
	}
	if v, ok := r.Int("clicks"); ok {
		m.Clicks = v
	}
}

// ErrorDetail is the inner object of every error body.
type ErrorDetail struct {
	Code    string
	Message string
}

func (m *ErrorDetail) IsNil() bool { return m == nil }

func (m *ErrorDetail) EncodeFields(w model.FieldWriter) {
	w.String("code", m.Code)
	w.String("message", m.Message)
}

func (m *ErrorDetail) DecodeFields(r model.FieldReader) {
	if v, ok := r.String("code"); ok {
		m.Code = v
	}
	if v, ok := r.String("message"); ok {
		m.Message = v
	}
}

// ErrorBody is the one error shape: {"error": {"code": "...", "message": "..."}}.
type ErrorBody struct {
	Error ErrorDetail
}

func (m *ErrorBody) IsNil() bool { return m == nil }

func (m *ErrorBody) EncodeFields(w model.FieldWriter) {
	w.Object("error", &m.Error)
}

func (m *ErrorBody) DecodeFields(r model.FieldReader) {
	r.Object("error", &m.Error)
}
