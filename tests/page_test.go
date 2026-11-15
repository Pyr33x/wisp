package tests

import (
	"crypto/sha256"
	"encoding/base64"
	"regexp"
	"strings"
	"testing"

	"github.com/pyr33x/wisp/gateway"
	"github.com/pyr33x/wisp/shared"
)

func TestPage_ServedAtRoot(t *testing.T) {
	g := newGateway()
	ctx := serve(g.register, request{method: "GET", path: "/"})
	wantStatus(t, ctx, 200)
	if ct := ctx.GetHeader(shared.HeaderContentType); ct != "text/html; charset=utf-8" {
		t.Fatalf("content-type = %q", ct)
	}
	if ctx.GetHeader("Content-Security-Policy") != gateway.PageCSP {
		t.Fatal("page must carry its CSP")
	}
	body := string(ctx.ResponseBody())
	for _, want := range []string{`id="shorten"`, `id="stats"`, `id="list"`, "/api/links"} {
		if !strings.Contains(body, want) {
			t.Fatalf("page is missing %s", want)
		}
	}
	if len(g.link.calls)+len(g.analytics.calls)+len(g.limiter.keys) != 0 {
		t.Fatal("serving the page must not touch any service")
	}
}

func TestPage_UnknownPathsAreJSON404(t *testing.T) {
	g := newGateway()
	for _, p := range []string{"/a/b", "/api/nope", "/abc1234/extra"} {
		wantError(t, serve(g.register, request{method: "GET", path: p}), 404, shared.ErrCodeNotFound)
	}
	if len(g.link.calls) != 0 {
		t.Fatal("an unknown path must not reach link")
	}
}

func TestPage_DoesNotShadowRedirectOrAPI(t *testing.T) {
	g := newGateway()
	g.link.answers["GET /api/links/abc1234"] = answer{status: 200, body: linkJSON}
	g.clicks.answers["POST /api/clicks/abc1234"] = answer{status: 204}
	wantStatus(t, serve(g.register, request{method: "GET", path: "/abc1234"}), 302)
	g.link.answers["GET /api/links/abc1234/exists"] = answer{status: 200, body: `{"exists":false}`}
	wantError(t, serve(g.register, request{method: "GET", path: "/api/links/abc1234/stats"}), 404, shared.ErrCodeNotFound)
}

// TestPage_CSPHashesMatchInlineBlocks fails when index.html is edited without
// updating gateway.PageCSP; otherwise browsers would silently refuse to run the
// page's script or apply its styles.
func TestPage_CSPHashesMatchInlineBlocks(t *testing.T) {
	html := gateway.IndexHTML()
	for _, tag := range []string{"style", "script"} {
		m := regexp.MustCompile(`(?s)<` + tag + `>(.*?)</` + tag + `>`).FindAllStringSubmatch(html, -1)
		if len(m) != 1 {
			t.Fatalf("want exactly one inline <%s>, got %d", tag, len(m))
		}
		sum := sha256.Sum256([]byte(m[0][1]))
		want := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
		if !strings.Contains(gateway.PageCSP, tag+"-src "+want) {
			t.Fatalf("PageCSP does not allow the current <%s> block; want %s", tag, want)
		}
	}
}

func TestPage_IsSelfContained(t *testing.T) {
	html := gateway.IndexHTML()
	for _, bad := range []string{"<script src", `rel="stylesheet"`, "@import", "innerHTML", "eval("} {
		if strings.Contains(html, bad) {
			t.Fatalf("page must not use %q", bad)
		}
	}
}
