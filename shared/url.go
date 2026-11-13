package shared

import "webtyp.com/fmt"

// MaxURLLength is the longest origin URL gateway accepts.
const MaxURLLength = 2048

const (
	schemeHTTP  = "http://"
	schemeHTTPS = "https://"
)

// URLError is a rejected origin URL: Code is the error code sent to the client.
type URLError struct {
	Code    string
	Message string
}

func (e *URLError) Error() string { return e.Message }

// ValidateOrigin trims raw and checks it is a usable redirect target: http or
// https, at most MaxURLLength bytes, with a host, and not pointing at wisp's
// own host (that would be a redirect loop). selfHost is the host of BASE_URL,
// as HostOf returns it. It returns the trimmed URL.
func ValidateOrigin(raw, selfHost string) (string, *URLError) {
	u := fmt.TrimSpace(raw)
	if u == "" {
		return "", &URLError{ErrCodeInvalidURL, "url is required"}
	}
	if len(u) > MaxURLLength {
		return "", &URLError{ErrCodeURLTooLong, "url must be at most 2048 characters"}
	}
	for i := 0; i < len(u); i++ {
		// Control characters and spaces are never valid in a URL, and a
		// header value carrying them is how response splitting starts.
		if u[i] <= ' ' || u[i] == 0x7f {
			return "", &URLError{ErrCodeInvalidURL, "url must not contain spaces or control characters"}
		}
	}
	host, ok := HostOf(u)
	if !ok {
		return "", &URLError{ErrCodeUnsupportedProto, "url must start with http:// or https://"}
	}
	if host == "" {
		return "", &URLError{ErrCodeMissingHost, "url must have a host"}
	}
	if selfHost != "" && host == selfHost {
		return "", &URLError{ErrCodeRedirectLoop, "url must not point at this shortener"}
	}
	return u, nil
}

// HostOf returns the lowercased host of an http(s) URL, without userinfo,
// port or trailing dot. ok is false when the scheme is not http or https.
func HostOf(u string) (host string, ok bool) {
	lower := fmt.ToLower(u)
	var rest string
	switch {
	case fmt.HasPrefix(lower, schemeHTTPS):
		rest = lower[len(schemeHTTPS):]
	case fmt.HasPrefix(lower, schemeHTTP):
		rest = lower[len(schemeHTTP):]
	default:
		return "", false
	}
	// Authority ends at the first path, query or fragment delimiter.
	end := len(rest)
	for i := 0; i < len(rest); i++ {
		if rest[i] == '/' || rest[i] == '?' || rest[i] == '#' {
			end = i
			break
		}
	}
	authority := rest[:end]
	// Userinfo is everything up to the LAST '@': "http://a@b@evil.com" goes to evil.com.
	if at := fmt.LastIndex(authority, "@"); at >= 0 {
		authority = authority[at+1:]
	}
	if fmt.HasPrefix(authority, "[") {
		// IPv6 literal: the host is the bracketed part, a port may follow.
		if rb := fmt.Index(authority, "]"); rb > 0 {
			return authority[:rb+1], true
		}
		return "", true
	}
	if colon := fmt.Index(authority, ":"); colon >= 0 {
		authority = authority[:colon]
	}
	authority = fmt.TrimSuffix(authority, ".")
	return authority, true
}

// NormalizeBaseURL trims spaces and every trailing slash, so joining it with
// "/" + code never yields a double slash.
func NormalizeBaseURL(base string) string {
	b := fmt.TrimSpace(base)
	for fmt.HasSuffix(b, "/") {
		b = b[:len(b)-1]
	}
	return b
}

// ShortURL builds the public short URL. Only link calls it.
func ShortURL(base, code string) string {
	return NormalizeBaseURL(base) + "/" + code
}
