//go:build wasm

package transport

import "syscall/js"

// Do calls the Workers runtime's global fetch.
//
// It deliberately does NOT use webtyp.com/fetch: that client runs
// js.Global().Call("fetch", ...) and js.Global().Call("setTimeout", ...).
// In a goflare Worker js.Global() is a Proxy over globalThis (it injects the
// per-instance "context"), so Call passes the Proxy as `this`, and workerd
// rejects fetch/setTimeout with "Illegal invocation". The thrown error kills
// the Go instance and every later request in the isolate fails. Invoke calls
// the function with an undefined `this`, which workerd accepts, and the
// timeout uses AbortSignal.timeout instead of setTimeout.
func (c *URLClient) Do(method, path string, body []byte) (int, []byte, error) {
	fetch := js.Global().Get("fetch")
	return doFetch(func(url string, init js.Value) js.Value {
		return fetch.Invoke(url, init)
	}, c.baseURL+path, method, c.secret, body, c.timeoutMs)
}
