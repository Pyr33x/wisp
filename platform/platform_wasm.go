//go:build wasm

package platform

import (
	"syscall/js"

	"webtyp.com/cloudflare/log"
	"webtyp.com/fmt"
)

// CryptoRandom fills b from crypto.getRandomValues, the Workers runtime's CSPRNG.
// Calling it directly avoids crypto/rand, which would drag stdlib into the binary.
func CryptoRandom(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	crypto := js.Global().Get("crypto")
	if crypto.IsUndefined() || crypto.IsNull() {
		return fmt.Err("crypto", "getRandomValues", "unavailable")
	}
	arr := js.Global().Get("Uint8Array").New(len(b))
	crypto.Call("getRandomValues", arr)
	js.CopyBytesToGo(b, arr)
	return nil
}

// NowMillis returns the current unix time in milliseconds (Date.now()).
func NowMillis() int64 {
	return int64(js.Global().Get("Date").Call("now").Float())
}

// EdgeLogger is shared.Logger backed by webtyp.com/cloudflare/log.
type EdgeLogger struct{}

func (EdgeLogger) Fail(status int, method, path string, err error) {
	log.Fail(status, method, path, err)
}

func (EdgeLogger) Reject(status int, method, path, reason string) {
	log.Reject(status, method, path, reason)
}

// Startup prints a fatal configuration error. main() returns right after, and
// goflare's worker.mjs turns that into a named error on every request.
func Startup(service string, err error) {
	js.Global().Get("console").Call("error", "wisp "+service+": "+err.Error())
}
