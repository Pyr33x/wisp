# Wisp

**Wisp** is a URL shortener written in Go and deployed to Cloudflare Workers as WebAssembly. It focuses on staying small, fast, and free — everything fits the Cloudflare Workers Free plan.

## Features

- ⚡ **Edge**: Three Go Workers compiled with TinyGo, built and deployed by [goflare](https://github.com/webtyp/goflare) — no Node.js, no Wrangler
- 🗄️ **Storage**: Each service owns its own [D1](https://developers.cloudflare.com/d1/) database, no shared tables
- 🔗 **Codes**: 7-character base62 codes from a cryptographically secure source, collisions retried on the `UNIQUE` constraint
- 📊 **Analytics**: Click counting with a single atomic upsert per redirect
- 🛡️ **Abuse protection**: Per-IP rate limiting on link creation, URL validation, redirect-loop detection
- 🔐 **Internal auth**: Internal Workers reject any request without the shared `X-Wisp-Internal` secret

## Architecture

| Worker | Visibility | Owns | Role |
|---|---|---|---|
| `wisp-gateway` | public (custom domain) | `wisp-gateway` D1 (rate limits) | Validates input, rate-limits, checks the admin token, orchestrates the other two |
| `wisp-link` | internal (workers.dev + secret) | `wisp-links` D1 | Creates, resolves and soft-deletes links; builds short URLs |
| `wisp-analytics` | internal (workers.dev + secret) | `wisp-analytics` D1 | Records clicks and returns counts |

All cross-service traffic is HTTP and flows through the gateway — link and analytics never call each other.

## Installation

```bash
git clone https://github.com/pyr33x/wisp.git
cd wisp
go install webtyp.com/goflare/cmd/goflare@v0.5.69
go install webtyp.com/devflow/cmd/gotest@v0.4.191
```

> TinyGo 0.41 supports Go up to 1.26; `go.mod` pins `go 1.26.8`. Run `goflare tinygo` to install TinyGo.

## API

| Method | Path | Description | Responses |
|---|---|---|---|
| `POST` | `/api/links` | Create a short link from `{"url": "..."}` | `201`, `400`, `429`, `502` |
| `GET` | `/{code}` | Redirect to the original URL and count the click | `302`, `404`, `502` |
| `GET` | `/api/links/{code}/stats` | Click count for a link | `200`, `404`, `502` |
| `DELETE` | `/api/links/{code}` | Soft-delete a link (`Authorization: Bearer <ADMIN_TOKEN>`) | `204`, `401`, `502` |

```bash
curl -X POST https://wisp.example.com/api/links -d '{"url":"https://example.org"}'
# {"code":"aZ3k9Qx","short_url":"https://wisp.example.com/aZ3k9Qx","origin":"https://example.org","created_at":1763000000000}
```

All errors share one shape:

```json
{"error": {"code": "rate_limited", "message": "too many links created, try again later"}}
```

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

### Notes on Rate Limiting
- **10 creates per minute per IP**, keyed on `CF-Connecting-IP`
- Exceeding it returns `429` with `Retry-After`
- If the rate-limit store fails, requests are **allowed** and the failure is logged

## Testing

```bash
gotest
```
Runs vet, staticcheck, race, coverage, and the `//go:build wasm` suite in a headless browser against a fake `context.env` and D1.

## Build

goflare builds `web/edge/main.go` from the working directory, so stage a Worker at the root first:

```bash
cp -R services/link/web web
PROJECT_NAME=wisp-link WORKER_NAME=wisp-link goflare build -env /dev/null
goflare size -env /dev/null
rm -rf web
```

## Deploy

Deploys run in GitHub Actions with `webtyp/goflare`, one workflow per Worker, triggered on pushes to `main`. Schema migrations run before each deploy via `go run ./cmd/migrate -service <name>`.

### GitHub
- **Secrets**: `CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ACCOUNT_ID`
- **Variables**: `WISP_DOMAIN`, `WISP_BASE_URL`, `WISP_LINK_URL`, `WISP_ANALYTICS_URL`

### Cloudflare
1. Create D1 databases `wisp-links`, `wisp-analytics`, `wisp-gateway`
2. Create the three Workers and add their secrets (`INTERNAL_SECRET`, `ADMIN_TOKEN`) before the first deploy
3. Keep workers.dev enabled on `wisp-link` and `wisp-analytics`
4. Deploy link and analytics before the gateway

## Contributing
Feel free to open issues or submit pull requests. Any help is appreciated!

## License
MIT @ Mehdi Parandak
