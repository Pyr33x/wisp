//go:build wasm

package transport

import (
	"syscall/js"

	"webtyp.com/env"
	"webtyp.com/fmt"
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
	return doFetch(func(url string, init js.Value) js.Value {
		return c.fetcher.Call("fetch", url, init) // `this` is the binding: correct
	}, bindingOrigin+path, method, c.secret, body, c.timeoutMs)
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
