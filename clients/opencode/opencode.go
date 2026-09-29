package opencode

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"nairid/clients"
	"nairid/core"
	"nairid/core/log"
)

type OpenCodeClient struct {
	// No permissionsMode needed as we only support `bypassPermissions` for now.
}

func NewOpenCodeClient() *OpenCodeClient {
	return &OpenCodeClient{}
}

func (c *OpenCodeClient) StartNewSession(prompt string, options *clients.OpenCodeOptions, onLine clients.ProgressCallback) (string, error) {
	log.Info("📋 Starting to create new OpenCode session")

	args := buildRunArgs("", modelFromOptions(options), prompt, opencodeSupportsStandalone())

	log.Info("Starting new OpenCode session with prompt: %s", prompt)
	log.Info("Command arguments: %v", args)

	timeout := clients.SessionTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := buildCommand(ctx, options, args)

	log.Info("Running OpenCode command (timeout: %s)", timeout)
	result, err := clients.RunCommandStreaming(ctx, cmd, onLine)
	if err != nil {
		return "", handleCommandError(ctx, err, "OpenCode", timeout)
	}

	log.Info("OpenCode command completed successfully, outputLength: %d", len(result))
	log.Info("📋 Completed successfully - created new OpenCode session")
	return result, nil
}

func (c *OpenCodeClient) ContinueSession(sessionID, prompt string, options *clients.OpenCodeOptions, onLine clients.ProgressCallback) (string, error) {
	log.Info("📋 Starting to continue OpenCode session: %s", sessionID)

	args := buildRunArgs(sessionID, modelFromOptions(options), prompt, opencodeSupportsStandalone())

	log.Info("Executing OpenCode command with sessionID: %s, prompt: %s", sessionID, prompt)
	log.Info("Command arguments: %v", args)

	timeout := clients.SessionTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := buildCommand(ctx, options, args)

	log.Info("Running OpenCode command (timeout: %s)", timeout)
	result, err := clients.RunCommandStreaming(ctx, cmd, onLine)
	if err != nil {
		return "", handleCommandError(ctx, err, "OpenCode", timeout)
	}

	log.Info("OpenCode command completed successfully, outputLength: %d", len(result))
	log.Info("📋 Completed successfully - continued OpenCode session")
	return result, nil
}

// handleCommandError converts a clients.CommandError to a core.ErrClaudeCommandErr,
// including timeout detection via context deadline.
func handleCommandError(ctx context.Context, err error, agentName string, timeout time.Duration) error {
	var cmdErr *clients.CommandError
	if errors.As(err, &cmdErr) {
		if ctx.Err() == context.DeadlineExceeded {
			log.Error("⏰ %s session timed out after %s", agentName, timeout)
			return &core.ErrClaudeCommandErr{
				Err:    fmt.Errorf("session timed out after %s: %w", timeout, cmdErr.Err),
				Output: cmdErr.Output,
			}
		}
		return &core.ErrClaudeCommandErr{
			Err:    cmdErr.Err,
			Output: cmdErr.Output,
		}
	}
	return err
}

// modelFromOptions safely extracts the model string from options.
func modelFromOptions(options *clients.OpenCodeOptions) string {
	if options != nil {
		return options.Model
	}
	return ""
}

// buildRunArgs constructs the argument list for `opencode run`.
//
// When standalone is true (OpenCode v2), `--standalone` is inserted so the run
// spins up its own private server instead of connecting to the shared
// background service. See opencodeSupportsStandalone for why this matters.
func buildRunArgs(sessionID, model, prompt string, standalone bool) []string {
	args := []string{"run"}
	if standalone {
		// v2 only: use a private per-invocation server (see opencodeSupportsStandalone).
		args = append(args, "--standalone")
	}
	if sessionID != "" {
		args = append(args, "--session", sessionID)
	}
	// Always use build mode until `acceptEdits` support is added.
	args = append(args, "--format", "json", "--agent", "build")
	if model != "" {
		args = append(args, "--model", model)
	}
	// Prompt is always the final positional argument.
	args = append(args, prompt)
	return args
}

var (
	standaloneOnce   sync.Once
	standaloneCached bool

	// runOpenCodeVersion is a package var so tests can stub the version probe.
	runOpenCodeVersion = func() (string, error) {
		out, err := exec.Command("opencode", "--version").CombinedOutput()
		return string(out), err
	}

	opencodeVersionRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)
)

// opencodeSupportsStandalone reports whether the installed OpenCode is v2+.
//
// OpenCode v2 is a client/server rewrite: `opencode run` connects to a shared
// background server (`opencode serve --service`) whose environment — including
// HTTP(S)_PROXY — is frozen when that server first starts. If anything starts
// that server without nairid's proxy env (e.g. a stray `opencode` invocation),
// every later job reuses the proxy-less server and its requests bypass the
// secret proxy, so secret placeholders are sent unresolved. Passing
// `--standalone` makes each run use its own private server carrying that
// process's proxy env, eliminating the shared-server contamination.
//
// OpenCode v1 has no background server and no `--standalone` flag, so it must
// keep its original command. Detection is cached for the process lifetime; on
// any probe/parse failure we assume v1 and omit the flag, so we never pass an
// unsupported flag to an older binary.
func opencodeSupportsStandalone() bool {
	standaloneOnce.Do(func() {
		standaloneCached = detectOpenCodeMajorAtLeast2(runOpenCodeVersion)
	})
	return standaloneCached
}

// detectOpenCodeMajorAtLeast2 parses `opencode --version` output and reports
// whether the major version is >= 2. Returns false (treat as v1) on any error
// or unparseable output.
func detectOpenCodeMajorAtLeast2(versionFn func() (string, error)) bool {
	out, err := versionFn()
	if err != nil {
		log.Error("Failed to probe OpenCode version (assuming v1, no --standalone): %v", err)
		return false
	}
	m := opencodeVersionRe.FindStringSubmatch(out)
	if m == nil {
		log.Info("Could not parse OpenCode version from %q (assuming v1)", strings.TrimSpace(out))
		return false
	}
	major, err := strconv.Atoi(m[1])
	if err != nil {
		return false
	}
	log.Info("Detected OpenCode major version %d (standalone=%t)", major, major >= 2)
	return major >= 2
}

// buildCommand creates the appropriate exec.Cmd with context based on options
func buildCommand(ctx context.Context, options *clients.OpenCodeOptions, args []string) *exec.Cmd {
	if options != nil && options.WorkDir != "" {
		log.Info("Using working directory: %s", options.WorkDir)
		return clients.BuildAgentCommandWithContextAndWorkDir(ctx, options.WorkDir, "opencode", args...)
	}
	return clients.BuildAgentCommandWithContext(ctx, "opencode", args...)
}
