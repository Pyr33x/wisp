//go:build !wasm

// Command migrate applies a wisp service's D1 schema from CI, through the
// Cloudflare D1 HTTP API (goflare.NewD1Migrator). No Worker ever runs DDL.
//
//	D1_DATABASE_ID=... CLOUDFLARE_ACCOUNT_ID=... CLOUDFLARE_API_TOKEN=... \
//	  go run ./cmd/migrate -service link
//
// The goflare action's pre-deploy step sets D1_DATABASE_ID from `goflare d1-id`.
// Every statement is idempotent (IF NOT EXISTS), so it runs on every deploy.
package main

import (
	"flag"
	"fmt"
	"os"

	"webtyp.com/goflare"

	"github.com/pyr33x/wisp/analytics"
	"github.com/pyr33x/wisp/gateway"
	"github.com/pyr33x/wisp/link"
)

func main() {
	service := flag.String("service", "", "service whose schema to apply: link, analytics or gateway")
	flag.Parse()

	stmts, err := Statements(*service)
	if err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(2)
	}
	conn, err := goflare.NewD1Migrator(
		os.Getenv("CLOUDFLARE_ACCOUNT_ID"),
		os.Getenv("D1_DATABASE_ID"),
		os.Getenv("CLOUDFLARE_API_TOKEN"),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
	for i, s := range stmts {
		if _, err := conn.Exec(s); err != nil {
			fmt.Fprintf(os.Stderr, "migrate %s: statement %d: %v\n", *service, i+1, err)
			os.Exit(1)
		}
	}
	fmt.Fprintf(os.Stderr, "migrate %s: %d statements applied\n", *service, len(stmts))
}

// Statements returns the DDL for one service, in order.
func Statements(service string) ([]string, error) {
	switch service {
	case "link":
		return []string{link.Schema}, nil
	case "analytics":
		return []string{analytics.Schema}, nil
	case "gateway":
		return []string{gateway.RateLimitSchema, gateway.RateLimitIndex}, nil
	default:
		return nil, fmt.Errorf("unknown service %q (want link, analytics or gateway)", service)
	}
}
