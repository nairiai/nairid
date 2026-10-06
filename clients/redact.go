package clients

import "regexp"

// urlCredentialsPattern matches a URL's userinfo (scheme://user[:password]@host).
var urlCredentialsPattern = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/@\s]+@`)

// RedactURLCredentials rewrites every scheme://user:pass@host occurrence to
// scheme://***@host so remote URLs (e.g. https://x-access-token:<token>@github.com/...)
// and command output that embeds them are safe to log. Input without embedded
// credentials is returned unchanged.
func RedactURLCredentials(s string) string {
	return urlCredentialsPattern.ReplaceAllString(s, "${1}***@")
}
