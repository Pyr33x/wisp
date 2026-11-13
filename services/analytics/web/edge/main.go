//go:build wasm

// wisp-analytics: internal Worker that owns the analytics table (D1 wisp-analytics).
package main

import (
	"webtyp.com/cloudflare/d1"
	"webtyp.com/cloudflare/edge"
	"webtyp.com/env"

	"github.com/pyr33x/wisp/analytics"
	"github.com/pyr33x/wisp/platform"
)

const (
	service   = "analytics"
	d1Binding = "DB"
)

func main() {
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

	svc := &analytics.Service{
		Store:  &analytics.SQLStore{DB: db.RawConn()},
		Log:    platform.EdgeLogger{},
		Secret: secret,
	}
	r := edge.NewRouter(edge.Config{})
	svc.Register(r)
	edge.Serve(r)
}
