//go:build wasm

package transport

import (
	"syscall/js"

	"webtyp.com/await"
	"webtyp.com/env"
	"webtyp.com/fmt"

	"github.com/pyr33x/wisp/shared"
)

// bindingOrigin is the URL origin used for service-binding requests. The host
// is ignored by the runtime; only the path reaches the target Worker.
const bindingOrigin = "https://internal"

// BindingClient calls another Worker through a Cloudflare service binding
// (env[name].fetch). The internal secret is still sent, so the target's
// RequireInternal check is the same whichever mode the caller uses.
type BindingClient struct {
	fetcher   js.Value
	secret    string
	timeoutMs int
}

func (c *BindingClient) Do(method, path string, body []byte) (int, []byte, error) {
	headers := js.Global().Get("Headers").New()
	headers.Call("set", shared.HeaderInternal, c.secret)
	init := js.Global().Get("Object").New()
	init.Set("method", method)
	init.Set("headers", headers)
	if len(body) > 0 && method != MethodGet {
		headers.Call("set", shared.HeaderContentType, shared.ContentTypeJSON)
		arr := js.Global().Get("Uint8Array").New(len(body))
		js.CopyBytesToJS(arr, body)
		init.Set("body", arr)
	}
	if c.timeoutMs > 0 {
		init.Set("signal", js.Global().Get("AbortSignal").Call("timeout", c.timeoutMs))
	}
	resp, err := await.Promise(c.fetcher.Call("fetch", bindingOrigin+path, init))
	if err != nil {
		return 0, nil, err
	}
	buf, err := await.Promise(resp.Call("arrayBuffer"))
	if err != nil {
		return 0, nil, err
	}
	arr := js.Global().Get("Uint8Array").New(buf)
	out := make([]byte, arr.Get("length").Int())
	js.CopyBytesToGo(out, arr)
	return resp.Get("status").Int(), out, nil
}

// FromEnv picks the transport for one downstream service from configuration:
// if the Worker has a service binding named bindingName (an object with a
// fetch method), calls go through it; otherwise urlKey must name an env var
// holding the target's base URL. Switching modes is a deploy-config change.
func FromEnv(bindingName, urlKey, secret string, timeoutMs int) (Client, error) {
	ctx := js.Global().Get("context")
	if !ctx.IsUndefined() && !ctx.IsNull() {
		b := ctx.Get("env").Get(bindingName)
		if b.Type() == js.TypeObject && b.Get("fetch").Type() == js.TypeFunction {
			return &BindingClient{fetcher: b, secret: secret, timeoutMs: timeoutMs}, nil
		}
	}
	base, ok := env.Lookup(urlKey)
	if !ok || base == "" {
		return nil, fmt.Err(urlKey, "is not set and there is no", bindingName, "service binding")
	}
	return NewURLClient(base, secret, timeoutMs), nil
}
