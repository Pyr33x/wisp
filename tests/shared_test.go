package tests

import (
	"strings"
	"testing"

	"github.com/pyr33x/wisp/shared"
)

func TestNewCode_LengthAndAlphabet(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		code, err := shared.NewCode(cryptoSource)
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != shared.CodeLength {
			t.Fatalf("len(%q) = %d", code, len(code))
		}
		if !shared.ValidCode(code) {
			t.Fatalf("%q is not base62", code)
		}
		seen[code] = true
	}
	if len(seen) < 199 {
		t.Fatalf("only %d distinct codes out of 200", len(seen))
	}
}

func TestNewCode_MapsBytesToAlphabet(t *testing.T) {
	code, err := shared.NewCode(seqRandom(0, 1, 9, 10, 35, 36, 61))
	if err != nil {
		t.Fatal(err)
	}
	if code != "019azAZ" {
		t.Fatalf("code = %q, want 019azAZ", code)
	}
}

func TestNewCode_RejectsBiasedBytes(t *testing.T) {
	// 248..255 would fold onto '0'..'7' if taken modulo 62; they must be skipped.
	code, err := shared.NewCode(seqRandom(255, 248, 62, 250))
	if err != nil {
		t.Fatal(err)
	}
	if code != "0000000" { // only 62 (→ index 0) is usable
		t.Fatalf("code = %q", code)
	}
}

func TestNewCode_BrokenSourceFails(t *testing.T) {
	if _, err := shared.NewCode(seqRandom(255)); err == nil {
		t.Fatal("a source that only yields rejected bytes must fail, not loop")
	}
	failing := func([]byte) error { return shared.ErrRandom }
	if _, err := shared.NewCode(failing); err == nil {
		t.Fatal("a failing source must fail")
	}
}

func TestValidCode(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"abcDEF1", true},
		{"0000000", true},
		{"abcDEF", false},
		{"abcDEF12", false},
		{"abc-EF1", false},
		{"abc EF1", false},
		{"", false},
		{"ábcdef", false},
	} {
		if got := shared.ValidCode(c.in); got != c.want {
			t.Errorf("ValidCode(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestSecureEqual(t *testing.T) {
	if !shared.SecureEqual("s3cret", "s3cret") {
		t.Fatal("equal strings")
	}
	for _, other := range []string{"s3creT", "s3cre", "s3cret!", ""} {
		if shared.SecureEqual("s3cret", other) {
			t.Fatalf("s3cret == %q", other)
		}
	}
}

func TestValidateOrigin(t *testing.T) {
	const self = "wisp.example.com"
	long := "https://a.com/" + strings.Repeat("x", shared.MaxURLLength)
	for _, c := range []struct {
		in, wantURL, wantCode string
	}{
		{"https://example.org/path?q=1", "https://example.org/path?q=1", ""},
		{"  http://example.org  ", "http://example.org", ""},
		{"HTTPS://Example.org", "HTTPS://Example.org", ""},
		{"http://[::1]:8080/x", "http://[::1]:8080/x", ""},
		{"", "", shared.ErrCodeInvalidURL},
		{"   ", "", shared.ErrCodeInvalidURL},
		{"ftp://example.org", "", shared.ErrCodeUnsupportedProto},
		{"javascript:alert(1)", "", shared.ErrCodeUnsupportedProto},
		{"example.org", "", shared.ErrCodeUnsupportedProto},
		{"https://", "", shared.ErrCodeMissingHost},
		{"https:///path", "", shared.ErrCodeMissingHost},
		{"https://user@/x", "", shared.ErrCodeMissingHost},
		{"https://exa mple.org", "", shared.ErrCodeInvalidURL},
		{"https://example.org/\r\nSet-Cookie:x", "", shared.ErrCodeInvalidURL},
		{long, "", shared.ErrCodeURLTooLong},
		{"https://wisp.example.com/abc1234", "", shared.ErrCodeRedirectLoop},
		{"https://WISP.example.com:443/x", "", shared.ErrCodeRedirectLoop},
		{"https://wisp.example.com./x", "", shared.ErrCodeRedirectLoop},
		{"https://me@wisp.example.com/x", "", shared.ErrCodeRedirectLoop},
		{"https://wisp.example.com@evil.org/x", "https://wisp.example.com@evil.org/x", ""},
		{"https://sub.wisp.example.com/x", "https://sub.wisp.example.com/x", ""},
	} {
		got, err := shared.ValidateOrigin(c.in, self)
		code := ""
		if err != nil {
			code = err.Code
		}
		if code != c.wantCode || got != c.wantURL {
			t.Errorf("ValidateOrigin(%q) = (%q, %q), want (%q, %q)", c.in, got, code, c.wantURL, c.wantCode)
		}
	}
}

func TestValidateOrigin_ExactlyMaxLengthAccepted(t *testing.T) {
	base := "https://a.com/"
	u := base + strings.Repeat("x", shared.MaxURLLength-len(base))
	if _, err := shared.ValidateOrigin(u, ""); err != nil {
		t.Fatalf("a %d-byte URL must be accepted: %v", len(u), err)
	}
}

func TestShortURL_NeverDoubleSlash(t *testing.T) {
	for _, base := range []string{"https://wisp.example.com", "https://wisp.example.com/", "https://wisp.example.com///", " https://wisp.example.com/ "} {
		if got := shared.ShortURL(base, "abc1234"); got != "https://wisp.example.com/abc1234" {
			t.Errorf("ShortURL(%q) = %q", base, got)
		}
	}
}

func TestHostOf(t *testing.T) {
	for in, want := range map[string]string{
		"https://Wisp.Example.com":        "wisp.example.com",
		"https://wisp.example.com:8443/a": "wisp.example.com",
		"http://u:p@host.io?x=1":          "host.io",
		"https://host.io#frag":            "host.io",
	} {
		got, ok := shared.HostOf(in)
		if !ok || got != want {
			t.Errorf("HostOf(%q) = %q,%v want %q", in, got, ok, want)
		}
	}
	if _, ok := shared.HostOf("mailto:x@y.z"); ok {
		t.Error("mailto must not be http(s)")
	}
}
