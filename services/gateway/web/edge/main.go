//go:build wasm

// wisp-gateway: the only public Worker. Worker-only goflare project (no
// web/public), so every path, including GET /{code}, reaches the Worker.
package main

import (
	"webtyp.com/cloudflare/d1"
	"webtyp.com/cloudflare/edge"
	"webtyp.com/env"

	"github.com/pyr33x/wisp/gateway"
	"github.com/pyr33x/wisp/platform"
	"github.com/pyr33x/wisp/transport"
)

const (
	service   = "gateway"
	d1Binding = "DB"

	// Service binding names, used if the deploy ever declares them; otherwise
	// the *_URL variables select URL mode. See transport.FromEnv.
	linkBinding      = "LINK"
	analyticsBinding = "ANALYTICS"
)

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
	// ADMIN_TOKEN may be absent: DELETE then answers 401 to everyone, and the
	// rest of the API keeps working.
	adminToken := env.Get("ADMIN_TOKEN")
	logger := platform.EdgeLogger{}
	if adminToken == "" {
		logger.Reject(401, "DELETE", "/api/links/{code}", "ADMIN_TOKEN is not set: every delete is refused")
	}

	linkClient, err := transport.FromEnv(linkBinding, "LINK_URL", secret, transport.DefaultTimeoutMs)
	if err != nil {
		platform.Startup(service, err)
		return
	}
	analyticsClient, err := transport.FromEnv(analyticsBinding, "ANALYTICS_URL", secret, transport.DefaultTimeoutMs)
	if err != nil {
		platform.Startup(service, err)
		return
	}
	clicksClient, err := transport.FromEnv(analyticsBinding, "ANALYTICS_URL", secret, transport.BestEffortTimeoutMs)
	if err != nil {
		platform.Startup(service, err)
		return
	}
	db, err := d1.NewEdge(d1Binding)
	if err != nil {
		platform.Startup(service, err)
		return
	}

	svc := &gateway.Service{
		Link:      linkClient,
		Analytics: analyticsClient,
		Clicks:    clicksClient,
		Limiter: &gateway.D1Limiter{
			DB:       db.RawConn(),
			Limit:    gateway.DefaultLimit,
			WindowMs: gateway.DefaultWindowMs,
			Random:   platform.CryptoRandom,
			Log:      logger,
		},
		BaseURL:    baseURL,
		AdminToken: adminToken,
		Now:        platform.NowMillis,
		Log:        logger,
	}
	r := edge.NewRouter(edge.Config{})
	svc.Register(r)
	edge.Serve(r)
}
