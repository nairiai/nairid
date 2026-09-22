package clients

import "testing"

func TestRedactURLCredentials(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "github pat in https remote",
			in:   "https://x-access-token:github_pat_11ABCDEF_ghijkl@github.com/owner/repo.git",
			want: "https://***@github.com/owner/repo.git",
		},
		{
			name: "installation token in https remote",
			in:   "https://x-access-token:ghs_secret123@github.com/owner/repo.git",
			want: "https://***@github.com/owner/repo.git",
		},
		{
			name: "plain https remote unchanged",
			in:   "https://github.com/owner/repo.git",
			want: "https://github.com/owner/repo.git",
		},
		{
			name: "ssh scp-style remote unchanged",
			in:   "git@github.com:owner/repo.git",
			want: "git@github.com:owner/repo.git",
		},
		{
			name: "credentials embedded in git error output",
			in:   "fatal: unable to access 'https://x-access-token:ghs_secret@github.com/owner/repo.git/': The requested URL returned error: 403",
			want: "fatal: unable to access 'https://***@github.com/owner/repo.git/': The requested URL returned error: 403",
		},
		{
			name: "empty string",
			in:   "",
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RedactURLCredentials(tc.in); got != tc.want {
				t.Errorf("RedactURLCredentials(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
