//go:build !wasm

package platform

import (
	"crypto/rand"
	"fmt"
	"os"
	"time"
)

// CryptoRandom fills b from crypto/rand.
func CryptoRandom(b []byte) error {
	_, err := rand.Read(b)
	return err
}

// NowMillis returns the current unix time in milliseconds.
func NowMillis() int64 {
	return time.Now().UnixMilli()
}

// StderrLogger is shared.Logger writing to stderr, for native runs.
type StderrLogger struct{}

func (StderrLogger) Fail(status int, method, path string, err error) {
	fmt.Fprintln(os.Stderr, "wisp", status, method, path, "-", err)
}

func (StderrLogger) Reject(status int, method, path, reason string) {
	fmt.Fprintln(os.Stderr, "wisp", status, method, path, "-", reason)
}
