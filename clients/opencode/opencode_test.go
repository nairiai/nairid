package opencode

import (
	"errors"
	"reflect"
	"testing"
)

func TestBuildRunArgs_NewSession_V1(t *testing.T) {
	got := buildRunArgs("", "opencode/kimi-k3", "hi", false)
	want := []string{"run", "--format", "json", "--agent", "build", "--model", "opencode/kimi-k3", "hi"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("v1 new session args = %v, want %v", got, want)
	}
}

func TestBuildRunArgs_NewSession_V2_HasStandalone(t *testing.T) {
	got := buildRunArgs("", "opencode/kimi-k3", "hi", true)
	want := []string{"run", "--standalone", "--format", "json", "--agent", "build", "--model", "opencode/kimi-k3", "hi"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("v2 new session args = %v, want %v", got, want)
	}
}

func TestBuildRunArgs_ContinueSession_V2(t *testing.T) {
	got := buildRunArgs("ses_123", "opencode/kimi-k3", "hi", true)
	want := []string{"run", "--standalone", "--session", "ses_123", "--format", "json", "--agent", "build", "--model", "opencode/kimi-k3", "hi"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("v2 continue args = %v, want %v", got, want)
	}
}

func TestBuildRunArgs_ContinueSession_V1(t *testing.T) {
	got := buildRunArgs("ses_123", "opencode/kimi-k3", "hi", false)
	want := []string{"run", "--session", "ses_123", "--format", "json", "--agent", "build", "--model", "opencode/kimi-k3", "hi"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("v1 continue args = %v, want %v", got, want)
	}
}

func TestBuildRunArgs_NoModel(t *testing.T) {
	got := buildRunArgs("", "", "hi", true)
	want := []string{"run", "--standalone", "--format", "json", "--agent", "build", "hi"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("no-model args = %v, want %v", got, want)
	}
	// v1 must never carry --standalone.
	v1 := buildRunArgs("", "", "hi", false)
	for _, a := range v1 {
		if a == "--standalone" {
			t.Fatalf("v1 args must not contain --standalone: %v", v1)
		}
	}
}

func TestDetectOpenCodeMajorAtLeast2(t *testing.T) {
	cases := []struct {
		name string
		out  string
		err  error
		want bool
	}{
		{"v2 with prefix", "opencode v2.0.18\n", nil, true},
		{"v2 bare", "2.0.18", nil, true},
		{"v3", "opencode v3.1.0", nil, true},
		{"v1 with prefix", "opencode v1.18.33", nil, false},
		{"v1 bare", "1.18.33\n", nil, false},
		{"probe error assumes v1", "", errors.New("exec: not found"), false},
		{"unparseable assumes v1", "some banner text", nil, false},
		{"empty assumes v1", "", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := detectOpenCodeMajorAtLeast2(func() (string, error) { return tc.out, tc.err })
			if got != tc.want {
				t.Errorf("detectOpenCodeMajorAtLeast2(%q, %v) = %t, want %t", tc.out, tc.err, got, tc.want)
			}
		})
	}
}
