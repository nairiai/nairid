package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeAgentIDFromRepoIdentifier(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "github https URL",
			in:   "github.com/owner/repo",
			want: "github.com-owner-repo",
		},
		{
			name: "gitlab nested groups",
			in:   "gitlab.com/group/subgroup/repo",
			want: "gitlab.com-group-subgroup-repo",
		},
		{
			name: "windows-style backslashes",
			in:   `host\owner\repo`,
			want: "host-owner-repo",
		},
		{
			name: "no separators",
			in:   "single-token",
			want: "single-token",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeAgentIDFromRepoIdentifier(tt.in)
			if got != tt.want {
				t.Errorf("sanitizeAgentIDFromRepoIdentifier(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// initGitRepoWithRemote creates a temp git repo with the given HTTPS remote URL
// and returns its path. Skips the test if `git` isn't available.
func initGitRepoWithRemote(t *testing.T, remoteURL string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"remote", "add", "origin", remoteURL},
		// Some git versions need a HEAD before they'll cooperate.
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}
	return dir
}

func clearAgentIDEnv(t *testing.T) {
	t.Helper()
	saved := map[string]string{}
	for _, k := range []string{"NAIRI_AGENT_ID", "EKSEC_AGENT_ID"} {
		saved[k] = os.Getenv(k)
		_ = os.Unsetenv(k)
	}
	t.Cleanup(func() {
		for k, v := range saved {
			if v == "" {
				_ = os.Unsetenv(k)
			} else {
				_ = os.Setenv(k, v)
			}
		}
	})
}

func TestResolveNairiAgentIDForStartup_PrefersEnvVar(t *testing.T) {
	clearAgentIDEnv(t)
	_ = os.Setenv("NAIRI_AGENT_ID", "explicit-id")
	t.Cleanup(func() { _ = os.Unsetenv("NAIRI_AGENT_ID") })

	got, err := resolveNairiAgentIDForStartup("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "explicit-id" {
		t.Errorf("expected explicit env var to win, got %q", got)
	}
}

func TestResolveNairiAgentIDForStartup_FallsBackToRepoIdentifier(t *testing.T) {
	clearAgentIDEnv(t)
	repoDir := initGitRepoWithRemote(t, "https://github.com/example-org/example-repo")

	got, err := resolveNairiAgentIDForStartup(repoDir)
	if err != nil {
		t.Fatalf("expected repo-derived ID, got error: %v", err)
	}
	want := "github.com-example-org-example-repo"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestResolveNairiAgentIDForStartup_DistinctReposGetDistinctIDs(t *testing.T) {
	clearAgentIDEnv(t)
	repoA := initGitRepoWithRemote(t, "https://github.com/org/repo-a")
	repoB := initGitRepoWithRemote(t, "https://github.com/org/repo-b")

	idA, err := resolveNairiAgentIDForStartup(repoA)
	if err != nil {
		t.Fatalf("repoA resolution: %v", err)
	}
	idB, err := resolveNairiAgentIDForStartup(repoB)
	if err != nil {
		t.Fatalf("repoB resolution: %v", err)
	}
	if idA == idB {
		t.Errorf("expected distinct IDs for distinct repos, got %q for both", idA)
	}
	// Concrete property #201 cares about: the namespacing key differs, so
	// ~/.eksec_worktrees/agent-{idA}/ and ~/.eksec_worktrees/agent-{idB}/ are
	// disjoint subtrees and the three reclaim/cleanup scans can't cross over.
	if strings.HasPrefix(idA, idB) || strings.HasPrefix(idB, idA) {
		t.Errorf("agent IDs must not be prefixes of each other: %q vs %q", idA, idB)
	}
}

func TestResolveNairiAgentIDForStartup_NoRepoNoEnvErrors(t *testing.T) {
	clearAgentIDEnv(t)
	// Pass a non-git directory as the repo flag — resolveRepositoryContext will
	// treat the explicit path as IsRepoMode=true, but git will fail to find a
	// remote, so we should still get an error pointing at NAIRI_AGENT_ID.
	nonGitDir := t.TempDir()

	_, err := resolveNairiAgentIDForStartup(nonGitDir)
	if err == nil {
		t.Fatal("expected error when NAIRI_AGENT_ID is unset and dir is not a git repo")
	}
	if !strings.Contains(err.Error(), "NAIRI_AGENT_ID") {
		t.Errorf("expected error to mention NAIRI_AGENT_ID, got: %v", err)
	}
}

// TestResolveNairiAgentIDForStartup_RepoIDIsFilesystemSafe verifies the value
// returned by the resolver can be safely used as a directory component.
func TestResolveNairiAgentIDForStartup_RepoIDIsFilesystemSafe(t *testing.T) {
	clearAgentIDEnv(t)
	repoDir := initGitRepoWithRemote(t, "https://github.com/example-org/example-repo")

	id, err := resolveNairiAgentIDForStartup(repoDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.ContainsAny(id, `/\`) {
		t.Errorf("resolved agent ID must not contain path separators, got %q", id)
	}
	// Round-trip: it should also be usable as a filename.
	probe := filepath.Join(t.TempDir(), "agent-"+id)
	if err := os.MkdirAll(probe, 0755); err != nil {
		t.Errorf("could not create dir with sanitized agent ID as a path component: %v", err)
	}
}
