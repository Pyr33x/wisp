![Wisp](docs/banner.jpg)

# Wisp

**Wisp** is a URL shortener written in Go and deployed to Cloudflare Workers as WebAssembly. It focuses on staying small, fast, and cheap — everything runs on the Cloudflare Workers Free plan.

🌐 **Live:** [wisp.pyr33x.top](https://wisp.pyr33x.top)

## Features

- ⚡ **Edge**: Three Go Workers compiled with TinyGo, built and deployed by [goflare](https://github.com/webtyp/goflare) — no Node.js, no Wrangler
- 🗄️ **Storage**: Each service owns its own [D1](https://developers.cloudflare.com/d1/) database, no shared tables
- 🔗 **Codes**: 7-character base62 codes from a cryptographically secure source, collisions retried on the `UNIQUE` constraint
- 📊 **Analytics**: Click counting with a single atomic upsert per redirect
- 🛡️ **Abuse protection**: Per-IP rate limiting on link creation, URL validation, redirect-loop detection
- 🔐 **Internal auth**: Internal Workers reject any request without the shared `X-Wisp-Internal` secret
- 🖥️ **Web UI**: Shorten, copy, look up clicks and keep a recent-links list at [wisp.pyr33x.top](https://wisp.pyr33x.top), served by the gateway itself
- 🪶 **Small**: ~225–265 KiB of wasm per Worker (~75–95 KiB gzipped)

## Try It

```bash
G=https://wisp.pyr33x.top

# shorten
curl -s -X POST $G/api/links -H 'Content-Type: application/json' -d '{"url":"https://github.com/Pyr33x/wisp"}'
# {"code":"ubRE08P","short_url":"https://wisp.pyr33x.top/ubRE08P","origin":"https://github.com/Pyr33x/wisp","created_at":1794725854373}

# follow (302 → origin)
curl -s -o /dev/null -D - $G/ubRE08P

# stats
curl -s $G/api/links/ubRE08P/stats
# {"code":"ubRE08P","clicks":1}
```

## Architecture

```
client ──> wisp-gateway (wisp.pyr33x.top) ──HTTP + X-Wisp-Internal──> wisp-link      (workers.dev) ── D1 wisp-links
                 │                          └─────────────────────> wisp-analytics (workers.dev) ── D1 wisp-analytics
                 └── D1 wisp-gateway (rate limits)
```

| Worker | Visibility | Owns | Role |
|---|---|---|---|
| `wisp-gateway` | public (custom domain) | `wisp-gateway` D1 (rate limits) | Validates input, rate-limits, checks the admin token, orchestrates the other two |
| `wisp-link` | internal (workers.dev + secret) | `wisp-links` D1 | Creates, resolves and soft-deletes links; builds short URLs |
| `wisp-analytics` | internal (workers.dev + secret) | `wisp-analytics` D1 | Records clicks and returns counts |

All cross-service traffic is HTTP and flows through the gateway — link and analytics never call each other.

### Why the gateway needs a custom domain
Cloudflare blocks a Worker from `fetch`ing another Worker **on the same zone** (error `1042`, which surfaces as a bare `404`). With every Worker on `workers.dev`, the gateway's internal calls never arrive. Serving the gateway on its own zone (`wisp.pyr33x.top`) while link and analytics stay on `workers.dev` makes every call cross zones, which is allowed.

goflare cannot declare service bindings yet. The transport already switches to them automatically if a `LINK` / `ANALYTICS` binding ever appears, so moving off public URLs is a config change.

## Installation

```bash
git clone https://github.com/pyr33x/wisp.git
cd wisp
go install webtyp.com/goflare/cmd/goflare@v0.5.69
go install webtyp.com/devflow/cmd/gotest@v0.4.191
```

> TinyGo 0.41 supports Go up to 1.26; `go.mod` pins `go 1.26.8`. Run `goflare tinygo` to install TinyGo.
> goflare v0.5.69 embeds `webtyp.com/cloudflare` v0.0.73 — keep both pinned together, goflare refuses to build on version skew.

## API

| Method | Path | Description | Responses |
|---|---|---|---|
| `GET` | `/` | Web UI | `200` |
| `POST` | `/api/links` | Create a short link from `{"url": "..."}` | `201`, `400`, `429`, `502` |
| `GET` | `/{code}` | Redirect to the original URL and count the click | `302`, `404`, `502` |
| `GET` | `/api/links/{code}/stats` | Click count for a link | `200`, `404`, `502` |
| `DELETE` | `/api/links/{code}` | Soft-delete a link (`Authorization: Bearer <ADMIN_TOKEN>`) | `204`, `401`, `502` |

Redirects are always `302` with `Cache-Control: private, max-age=0` — never `301`, so browsers can't cache them and skip the click count.

All errors share one shape:

```json
{"error": {"code": "rate_limited", "message": "too many links created, try again later"}}
```

| Status | Meaning |
|---|---|
| `400` | Invalid input (`invalid_json`, `invalid_url`, `url_too_long`, `unsupported_scheme`, `missing_host`, `redirect_loop`) |
| `401` | Missing or wrong admin token / internal secret |
| `404` | Unknown or deleted link |
| `429` | Rate limited, see `Retry-After` |
| `502` | A downstream Worker failed (details are logged, never returned) |

## Usage

Each Worker lives in `services/<name>/` and has its own `.env.example`.

### Environment Variables

| Variable | Worker | Description | Kind |
|---|---|---|---|
| `BASE_URL` | gateway, link | Public gateway origin; short URLs are `BASE_URL/code` | var |
| `LINK_URL` | gateway | workers.dev origin of `wisp-link` | var |
| `ANALYTICS_URL` | gateway | workers.dev origin of `wisp-analytics` | var |
| `INTERNAL_SECRET` | all | Shared secret for internal calls | Worker secret |
| `ADMIN_TOKEN` | gateway | Bearer token for `DELETE` | Worker secret |
| `D1_NAME` / `D1_BINDING` | all | D1 database name and binding (`DB`) | goflare config |
| `DOMAIN` | gateway | Custom domain the gateway is served on | goflare config |

Worker secrets must be type **Secret** in the dashboard — goflare replaces plain-text and JSON variables on every deploy.

### Notes on Rate Limiting
- **10 creates per minute per IP**, keyed on `CF-Connecting-IP`
- Exceeding it returns `429` with `Retry-After`
- If the rate-limit store fails, requests are **allowed** and the failure is logged
- For flood protection on top, add a Cloudflare rate limiting rule for the gateway's hostname (Security → WAF → Rate limiting rules), e.g. 100 requests / 10 s per IP

## Testing

```bash
gotest
```

Runs `go vet`, staticcheck, `go test -race` with coverage, and the `//go:build wasm` suite in headless Chrome.

| Suite | Covers |
|---|---|
| Native (`tests/`) | Code generation and bias, URL validation, `BASE_URL` joining, constant-time compare, every handler of all three services through a router that enforces the real gate and middleware order, SQL statements, rate-limit windows, fail-open, transport timeouts |
| Wasm (`tests/edge_wasm_test.go`) | The real `webtyp.com/cloudflare` stack (`d1.NewEdge`, `env`, edge router) against a fake `context.env` and a fake D1 that speaks the D1 JS API, `crypto.getRandomValues`, transport selection, and a regression test that `fetch` is never called with goflare's global Proxy as `this` |

### Smoke test the live deployment

```bash
G=https://wisp.pyr33x.top
curl -s -X POST $G/api/links -d '{"url":"ftp://example.org"}'               # 400 unsupported_scheme
curl -s -X POST $G/api/links -d "{\"url\":\"$G/abc\"}"                      # 400 redirect_loop
curl -s $G/zzzzzzz                                                           # 404 not_found
curl -s -X DELETE $G/api/links/zzzzzzz                                       # 401 unauthorized
curl -s https://wisp-link.<subdomain>.workers.dev/api/links/zzzzzzz         # 401, internal Workers need the secret
for i in $(seq 1 11); do curl -s -o /dev/null -w "%{http_code} " -X POST $G/api/links -d '{"url":"https://example.org"}'; done  # ... 429
```

## Build

goflare builds `web/edge/main.go` from the working directory, so stage a Worker at the root first:

```bash
cp -R services/link/web web
PROJECT_NAME=wisp-link WORKER_NAME=wisp-link goflare build -env /dev/null
goflare size -env /dev/null
rm -rf web
```

## Deploy

Deploys run in GitHub Actions with `webtyp/goflare@v0.5.69`, one workflow per Worker, triggered on pushes to `master` that touch that Worker or the code it compiles in. Every deploy runs the full CI first, then migrates its D1 database via `go run ./cmd/migrate -service <name>`, then uploads.

### GitHub
- **Secrets**: `CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ACCOUNT_ID`
- **Variables**: `WISP_BASE_URL`, `WISP_LINK_URL`, `WISP_ANALYTICS_URL`
- **Optional variable** `WISP_DOMAIN`: set it for one gateway deploy to attach the custom domain, then delete it. While it is set, goflare also manages a zone rate-limit rule, and that step fails with account-owned (`cfat_…`) tokens

### Cloudflare
1. Add a domain to the account and pick the gateway hostname (e.g. `wisp.pyr33x.top`)
2. Create D1 databases `wisp-links`, `wisp-analytics`, `wisp-gateway`
3. Create an API token with:
   - Account: **Workers Scripts Edit**, **D1 Edit**
   - Zone (your domain), only while `WISP_DOMAIN` is set: **Zone Read**, **Workers Routes Edit**, **Zone WAF Edit**
4. Create the three Workers and add their secrets (`INTERNAL_SECRET` on all, `ADMIN_TOKEN` on the gateway) **before** the first deploy
5. Keep workers.dev enabled on `wisp-link` and `wisp-analytics`; never put them on the gateway's domain
6. Deploy link and analytics before the gateway; with `WISP_DOMAIN` set, the gateway deploy attaches the domain (or add it once under the Worker's **Settings → Domains & Routes**)

### Troubleshooting

| Symptom | Cause |
|---|---|
| `error code: 1101` on every request | The Worker's `main()` stopped at startup — a missing secret or variable; the Worker logs name it |
| Gateway returns `502`, link logs show nothing | Gateway is on the same zone as link/analytics (`1042`); serve it from a custom domain |
| `webtyp/cloudflare version skew` | `go.mod` and the goflare action drifted apart; keep v0.0.73 ↔ v0.5.69 |
| `rate limit: … HTTP 403` at the end of a gateway deploy | goflare's zone rate-limit step, which rejects account-owned tokens; the Worker is already live. Delete `WISP_DOMAIN` once the domain is attached |

## Contributing
Feel free to open issues or submit pull requests. Any help is appreciated!

## License
MIT @ Mehdi Parandak
