// Package gateway is wisp's only public Worker. It validates input, rate-limits
// creates, checks the admin token, and orchestrates link and analytics: link
// and analytics never call each other (two workers.dev Workers calling each
// other hit Cloudflare error 1042 without a compatibility flag goflare cannot
// set), so every cross-service flow runs from here.
package gateway

import (
	"webtyp.com/fmt"
	"webtyp.com/router"

	"github.com/pyr33x/wisp/shared"
	"github.com/pyr33x/wisp/transport"
)

// unknownIP buckets requests without CF-Connecting-IP. Cloudflare always sets
// it in production; it is missing only when running outside Cloudflare.
const unknownIP = "unknown"

// Service holds gateway's dependencies. Every field is required.
type Service struct {
	Link      transport.Client
	Analytics transport.Client
	// Clicks is the analytics client used for click recording, with a short
	// timeout: a slow or failing analytics never holds a redirect longer.
	Clicks     transport.Client
	Limiter    Limiter
	BaseURL    string // public origin of this Worker; links to it are refused
	AdminToken string // ADMIN_TOKEN; empty denies every DELETE
	Now        func() int64
	Log        shared.Logger
}

// Register mounts gateway's routes on r.
func (s *Service) Register(r router.Router) {
	r.Post(shared.PathLinks, s.create).Public()
	r.Get(shared.PathLinkStats, s.stats).Public()
	r.Delete(shared.PathLink, s.remove).Public()
	r.Get(shared.PathRedirect, s.redirect).Public()
	r.Get(shared.PathRoot, s.page).Public()
}

func (s *Service) create(ctx router.Context) {
	ip := ctx.GetHeader(shared.HeaderConnectingIP)
	if ip == "" {
		ip = unknownIP
	}
	d, err := s.Limiter.Hit(ip, s.Now())
	if err != nil {
		// Fail open: a storage hiccup must not take link creation down.
		s.Log.Fail(201, ctx.Method(), ctx.Path(), fmt.Err("rate limit storage failed, allowing:", err.Error()))
	} else if !d.Allowed {
		s.Log.Reject(429, ctx.Method(), ctx.Path(), "rate limited")
		ctx.SetHeader(shared.HeaderRetryAfter, fmt.Convert(d.RetryAfter).String())
		shared.WriteError(ctx, 429, shared.ErrCodeRateLimited, shared.MsgRateLimited)
		return
	}

	var in shared.CreateLinkRequest
	if err := ctx.Decode(&in); err != nil {
		shared.WriteError(ctx, 400, shared.ErrCodeInvalidJSON, shared.MsgInvalidJSON)
		return
	}
	selfHost, _ := shared.HostOf(s.BaseURL)
	origin, verr := shared.ValidateOrigin(in.URL, selfHost)
	if verr != nil {
		shared.WriteError(ctx, 400, verr.Code, verr.Message)
		return
	}

	var out shared.Link
	if _, err := transport.Call(s.Link, transport.MethodPost, shared.PathLinks,
		&shared.CreateLinkRequest{URL: origin}, &out); err != nil {
		s.upstreamFailed(ctx, err)
		return
	}
	shared.WriteJSON(ctx, 201, &out)
}

func (s *Service) redirect(ctx router.Context) {
	code := ctx.Param(shared.ParamCode)
	if !shared.ValidCode(code) {
		shared.WriteError(ctx, 404, shared.ErrCodeNotFound, shared.MsgNotFound)
		return
	}
	var l shared.Link
	status, err := transport.Call(s.Link, transport.MethodGet, shared.PathLinksPrefix+code, nil, &l)
	if status == 404 {
		shared.WriteError(ctx, 404, shared.ErrCodeNotFound, shared.MsgNotFound)
		return
	}
	if err != nil {
		s.upstreamFailed(ctx, err)
		return
	}

	// Best effort: bounded by the Clicks client's timeout, errors only logged.
	// There is no waitUntil in the goflare runtime, so this runs inline.
	if _, err := transport.Call(s.Clicks, transport.MethodPost, shared.PathClickPrefix+code, nil, nil); err != nil {
		s.Log.Fail(302, ctx.Method(), ctx.Path(), fmt.Err("click not recorded:", err.Error()))
	}

	ctx.SetHeader(shared.HeaderLocation, l.Origin)
	// Never 301 and never cacheable: every click must reach this Worker to be counted.
	ctx.SetHeader(shared.HeaderCacheControl, shared.CacheControlNoRedirect)
	ctx.WriteStatus(302)
}

func (s *Service) stats(ctx router.Context) {
	code := ctx.Param(shared.ParamCode)
	if !shared.ValidCode(code) {
		shared.WriteError(ctx, 404, shared.ErrCodeNotFound, shared.MsgNotFound)
		return
	}
	var ex shared.Exists
	if _, err := transport.Call(s.Link, transport.MethodGet, shared.PathLinksPrefix+code+shared.SuffixExists, nil, &ex); err != nil {
		s.upstreamFailed(ctx, err)
		return
	}
	if !ex.Exists {
		shared.WriteError(ctx, 404, shared.ErrCodeNotFound, shared.MsgNotFound)
		return
	}
	var st shared.Stats
	if _, err := transport.Call(s.Analytics, transport.MethodGet, shared.PathStatsPrefix+code, nil, &st); err != nil {
		s.upstreamFailed(ctx, err)
		return
	}
	shared.WriteJSON(ctx, 200, &shared.Stats{Code: code, Clicks: st.Clicks})
}

func (s *Service) remove(ctx router.Context) {
	if !s.authorizedAdmin(ctx.GetHeader(shared.HeaderAuthorization)) {
		s.Log.Reject(401, ctx.Method(), ctx.Path(), "missing or invalid admin token")
		shared.WriteError(ctx, 401, shared.ErrCodeUnauthorized, shared.MsgUnauthorized)
		return
	}
	code := ctx.Param(shared.ParamCode)
	if !shared.ValidCode(code) {
		// Deleting is idempotent; a code that cannot exist is already gone.
		shared.WriteNoContent(ctx)
		return
	}
	if _, err := transport.Call(s.Link, transport.MethodDelete, shared.PathLinksPrefix+code, nil, nil); err != nil {
		s.upstreamFailed(ctx, err)
		return
	}
	shared.WriteNoContent(ctx)
}

func (s *Service) authorizedAdmin(header string) bool {
	if s.AdminToken == "" || !fmt.HasPrefix(header, shared.BearerPrefix) {
		return false
	}
	return shared.SecureEqual(header[len(shared.BearerPrefix):], s.AdminToken)
}

// upstreamFailed logs the real cause and sends the client a generic 502.
func (s *Service) upstreamFailed(ctx router.Context, err error) {
	s.Log.Fail(502, ctx.Method(), ctx.Path(), err)
	shared.WriteError(ctx, 502, shared.ErrCodeUpstream, shared.MsgUpstream)
}
