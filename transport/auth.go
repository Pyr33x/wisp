package transport

import (
	"webtyp.com/router"

	"github.com/pyr33x/wisp/shared"
)

// RequireInternal is the middleware every route of an internal Worker sits
// behind. A request whose X-Wisp-Internal header does not match secret
// (constant-time) gets 401. An empty secret denies everything: a Worker
// deployed without INTERNAL_SECRET must never be open.
func RequireInternal(secret string, logger shared.Logger) router.Middleware {
	return func(next router.HandlerFunc) router.HandlerFunc {
		return func(ctx router.Context) {
			got := ctx.GetHeader(shared.HeaderInternal)
			if secret == "" || got == "" || !shared.SecureEqual(got, secret) {
				logger.Reject(401, ctx.Method(), ctx.Path(), "missing or invalid "+shared.HeaderInternal)
				shared.WriteError(ctx, 401, shared.ErrCodeUnauthorized, shared.MsgUnauthorized)
				return
			}
			next(ctx)
		}
	}
}
