// Package platform is the small seam between wisp's build-agnostic code and the
// runtime it runs on: the clock, the secure random source and the logger.
// On wasm (inside a Worker) it calls the JS runtime through syscall/js; on
// native builds (tests, CI tools) it uses the standard library.
package platform
