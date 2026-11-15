//go:build wasm

package transport

import (
	"syscall/js"

	"webtyp.com/await"

	"github.com/pyr33x/wisp/shared"
)

// doFetch performs one internal request through call (the global fetch or a
// service binding's fetch) and reads the whole answer.
func doFetch(call func(url string, init js.Value) js.Value, url, method, secret string, body []byte, timeoutMs int) (int, []byte, error) {
	headers := js.Global().Get("Headers").New()
	headers.Call("set", shared.HeaderInternal, secret)
	init := js.Global().Get("Object").New()
	init.Set("method", method)
	init.Set("headers", headers)
	if len(body) > 0 && method != MethodGet {
		headers.Call("set", shared.HeaderContentType, shared.ContentTypeJSON)
		arr := js.Global().Get("Uint8Array").New(len(body))
		js.CopyBytesToJS(arr, body)
		init.Set("body", arr)
	}
	if timeoutMs > 0 {
		// Called on the AbortSignal constructor itself, so `this` is correct.
		init.Set("signal", js.Global().Get("AbortSignal").Call("timeout", timeoutMs))
	}
	resp, err := await.Promise(call(url, init))
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
