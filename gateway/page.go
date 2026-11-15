package gateway

import (
	_ "embed"

	"webtyp.com/router"

	"github.com/pyr33x/wisp/shared"
)

// indexHTML is the single-page UI served at "/". It is one self-contained file
// (no external fonts, scripts or images) embedded into the gateway's wasm.
//
//go:embed index.html
var indexHTML string

// PageCSP allows exactly the page's one inline <style> and one inline <script>
// by hash, and lets the script talk only to this origin. The hashes are checked
// against index.html by TestPage_CSPHashesMatchInlineBlocks: editing the page
// without updating them fails the build, never the browser silently.
const PageCSP = "default-src 'none'; " +
	"style-src 'sha256-Tgfvmk+VyBVjPr9iErC7GMzE0czFWzuznSqGaXuHlhI='; " +
	"script-src 'sha256-nkUAr0tcgGSjNJjEoQC8zhW9MhRZWgK2ttMz75mtZo0='; " +
	"connect-src 'self'; img-src data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// IndexHTML returns the embedded page (exported for tests).
func IndexHTML() string { return indexHTML }

const (
	pageContentType = "text/html; charset=utf-8"
	// Short browser/edge caching: a redeploy shows up within minutes, and a
	// cached page costs no Worker request.
	pageCacheControl = "public, max-age=300"
)

// page serves the UI at exactly "/". The "/" pattern matches every path the
// more specific routes do not, so anything else is answered with the JSON 404
// the rest of the API uses.
func (s *Service) page(ctx router.Context) {
	if ctx.Path() != "/" {
		shared.WriteError(ctx, 404, shared.ErrCodeNotFound, shared.MsgNotFound)
		return
	}
	ctx.SetHeader(shared.HeaderContentType, pageContentType)
	ctx.SetHeader(shared.HeaderCacheControl, pageCacheControl)
	ctx.SetHeader("Content-Security-Policy", PageCSP)
	ctx.SetHeader("X-Content-Type-Options", "nosniff")
	ctx.SetHeader("Referrer-Policy", "no-referrer")
	ctx.WriteStatus(200)
	ctx.Write([]byte(indexHTML))
}
