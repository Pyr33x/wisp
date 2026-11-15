package shared

import (
	"webtyp.com/model"
	"webtyp.com/router"
)

// Header names.
const (
	HeaderInternal         = "X-Wisp-Internal"
	HeaderAuthorization    = "Authorization"
	HeaderContentType      = "Content-Type"
	HeaderLocation         = "Location"
	HeaderCacheControl     = "Cache-Control"
	HeaderRetryAfter       = "Retry-After"
	HeaderConnectingIP     = "CF-Connecting-IP"
	ContentTypeJSON        = "application/json"
	CacheControlNoRedirect = "private, max-age=0"
	BearerPrefix           = "Bearer "
)

// Route patterns. Internal services and gateway share the /api/links prefix on
// purpose: the contract is defined once, here.
const (
	PathLinks       = "/api/links"
	PathLink        = "/api/links/{code}"
	PathLinkExists  = "/api/links/{code}/exists"
	PathLinkStats   = "/api/links/{code}/stats"
	PathClick       = "/api/clicks/{code}"
	PathStats       = "/api/stats/{code}"
	PathRedirect    = "/{code}"
	PathRoot        = "/"
	ParamCode       = "code"
	PathLinksPrefix = "/api/links/"
	PathClickPrefix = "/api/clicks/"
	PathStatsPrefix = "/api/stats/"
	SuffixExists    = "/exists"
)

// Error codes (the "code" field of every error body).
const (
	ErrCodeInvalidJSON      = "invalid_json"
	ErrCodeInvalidURL       = "invalid_url"
	ErrCodeURLTooLong       = "url_too_long"
	ErrCodeUnsupportedProto = "unsupported_scheme"
	ErrCodeMissingHost      = "missing_host"
	ErrCodeRedirectLoop     = "redirect_loop"
	ErrCodeInvalidCode      = "invalid_code"
	ErrCodeNotFound         = "not_found"
	ErrCodeUnauthorized     = "unauthorized"
	ErrCodeRateLimited      = "rate_limited"
	ErrCodeUpstream         = "upstream_error"
	ErrCodeInternal         = "internal_error"
)

// Messages that are sent to clients. Internal causes never reach them.
const (
	MsgInvalidJSON  = "request body must be a JSON object"
	MsgInvalidCode  = "code must be 7 characters of 0-9, a-z, A-Z"
	MsgNotFound     = "link not found"
	MsgUnauthorized = "missing or invalid credentials"
	MsgRateLimited  = "too many links created, try again later"
	MsgUpstream     = "a downstream service failed"
	MsgInternal     = "unexpected error"
)

// Logger is how handlers report what went wrong. On the edge it is backed by
// webtyp.com/cloudflare/log; tests capture it.
type Logger interface {
	// Fail records a failure that is ours (5xx, or a swallowed best-effort error).
	Fail(status int, method, path string, err error)
	// Reject records a request refused on purpose (4xx).
	Reject(status int, method, path, reason string)
}

// WriteJSON writes v as the JSON body with the given status.
func WriteJSON(ctx router.Context, status int, v model.Encodable) {
	ctx.SetHeader(HeaderContentType, ContentTypeJSON)
	ctx.WriteStatus(status)
	if err := ctx.Encode(v); err != nil {
		// The status is already out; the body is what failed. Nothing better
		// can be sent, and the caller sees a truncated body.
		ctx.Write([]byte("{}"))
	}
}

// WriteError writes the one error shape with the given status.
func WriteError(ctx router.Context, status int, code, message string) {
	WriteJSON(ctx, status, &ErrorBody{Error: ErrorDetail{Code: code, Message: message}})
}

// WriteNoContent answers 204 with no body.
func WriteNoContent(ctx router.Context) {
	ctx.WriteStatus(204)
}
