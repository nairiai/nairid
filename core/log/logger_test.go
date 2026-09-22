package log

import "testing"

func TestSanitizeScrubsAccessToken(t *testing.T) {
	cases := map[string]string{
		"remote: https://x-access-token:ghs_abc123@github.com/org/repo.git":                 "remote: https://x-access-token:REDACTED@github.com/org/repo.git",
		"fatal: unable to access 'https://x-access-token:ghs_abc@github.com/o/r.git/': 403": "fatal: unable to access 'https://x-access-token:REDACTED@github.com/o/r.git/': 403",
		"https://github.com/org/repo.git":                                                   "https://github.com/org/repo.git",
		"plain message":                                                                     "plain message",
	}
	for in, want := range cases {
		if got := sanitize(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}
