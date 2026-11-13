//go:build wasm

// wisp-link: internal Worker that owns the links table (D1 wisp-links).
package main

import (
	"webtyp.com/cloudflare/d1"
	"webtyp.com/cloudflare/edge"
	"webtyp.com/env"

	"github.com/pyr33x/wisp/link"
	"github.com/pyr33x/wisp/platform"
)

const (
	service   = "link"
	d1Binding = "DB"
)

// main runs once per isolate. A missing binding or variable returns early;
// goflare's worker.mjs then fails every request with a named error instead
// of serving a half-configured Worker.
func main() {
	baseURL, err := env.Require("BASE_URL")
	if err != nil {
		platform.Startup(service, err)
		return
	}
	secret, err := env.Require("INTERNAL_SECRET")
	if err != nil {
		platform.Startup(service, err)
		return
	}
	db, err := d1.NewEdge(d1Binding)
	if err != nil {
		platform.Startup(service, err)
		return
	}

	svc := &link.Service{
		Store:   &link.SQLStore{DB: db.RawConn()},
		BaseURL: baseURL,
		Random:  platform.CryptoRandom,
		Now:     platform.NowMillis,
		Log:     platform.EdgeLogger{},
		Secret:  secret,
	}
	r := edge.NewRouter(edge.Config{})
	svc.Register(r)
	edge.Serve(r)
}
