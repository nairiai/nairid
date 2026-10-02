## [v0.0.122] - 2026-10-02

### Bugfixes

- Keep ask mode when a conversation is restarted or recovered ([#226](https://github.com/nairiai/nairid/pull/226))
  - A reply that restarts a conversation with no CLI session now carries the job's mode; an empty mode used to run as execute (no ask-mode instructions, execute-only post-turn steps)
  - The job state saved before a follow-up runs now keeps `Mode`, so a follow-up recovered after a crash stays in ask mode
  - Crash recovery of an unstarted conversation passes the mode too

## [v0.0.121] - 2026-10-02

### Bugfixes

- Keep the reasoning effort on every turn of a conversation ([#225](https://github.com/nairiai/nairid/pull/225))
  - `AppState.GetJobData` / `GetAllJobs` copied `JobData` field by field and dropped `ReasoningEffort`, so every follow-up turn ran at the model default on all three CLIs; both now copy the whole struct
  - A reply that is upgraded to a conversation start (no CLI session yet) and crash recovery of an unstarted conversation now carry the job's level
  - `JobData` also stores `effort_model`, the model the level was checked against; every turn re-resolves the level against the model the agent runs now, so a model switch drops it (Codex fails on unsupported levels) and switching back restores it
  - Follow-up turns now log `Reasoning effort for job <id>: <level|model default>` too

## [v0.0.120] - 2026-10-02

### Features

- Send the description Claude writes with each Bash command ([#224](https://github.com/nairiai/nairid/pull/224))
  - Progress events carry a new optional `tool_description` field on both the tool start and its paired result, holding the short "what this command does" line Claude Code attaches to every `Bash` call
  - `tool_input` still holds the raw command, so existing readers are unchanged; only `Bash` sends a description
  - The backend uses it to show what each step is for in the Slack checklist instead of the bare program name; backends that don't know the field ignore it

## [v0.0.119] - 2026-10-01

### Features

- Pass a per-conversation reasoning effort to the agent CLI ([#223](https://github.com/nairiai/nairid/pull/223))
  - `start_conversation` payloads may carry `reasoning_effort` and `reasoning_effort_model`; nairid maps the level to each CLI's flag (`claude --effort`, `codex -c model_reasoning_effort=`, `opencode --variant`) and passes it on every run, new and resumed
  - The level is fixed at conversation start and persisted in `JobData`, so replies (including after a restart) reuse it
  - Unknown levels are dropped with a warning, and the level is applied only when `reasoning_effort_model` matches the agent's own `--model`; with no field in the payload the CLI command is unchanged
  - Each new job logs `Reasoning effort for job <id>: <level|model default>`

### Bugfixes

- Stop the idle check from reverting completed job status and leaking worker slots ([#222](https://github.com/nairiai/nairid/pull/222))
  - `checkJobIdleness` wrote a whole `JobData` snapshot back to cache the PR ID, which could revert a just-persisted `completed` status to `in_progress` and leave the job processor holding a worker slot until the 25h idle eviction
  - New `AppState.SetJobPullRequestID` updates only that field under the lock and does not recreate a job removed in the meantime
- Bypass the proxy for loopback and run OpenCode v2 in standalone mode ([#221](https://github.com/nairiai/nairid/pull/221))
  - `InjectProxyEnv` now always adds `localhost,127.0.0.1,::1` to `NO_PROXY`, so OpenCode v2's local background server is reachable
  - On OpenCode v2+ nairid passes `run --standalone` so each run gets its own server with the caller's proxy env; OpenCode v1 commands are unchanged

## [v0.0.118] - 2026-09-22

### Security

- Redact GitHub token from logs and user-facing error messages ([#220](https://github.com/nairiai/nairid/pull/220))
  - Once the remote is rewritten to `https://x-access-token:<token>@github.com/...`, every log line that printed the remote URL leaked the token in plaintext. `RedactURLCredentials` now strips the userinfo at every remote-URL log site in the git client (`getRawRemoteURL`, `GetRemoteURL`, `GetRepositoryIdentifier`, `ValidateRemoteAccess`, `UpdateRemoteURLWithToken`, `GetRemoteURLInWorktree`) and in the messages built by `parseRemoteAccessError`
  - Added a logger-level scrub that removes `x-access-token:...@` from every emitted line as a safety net for git's own error output at call sites the code does not control
  - Outbound error messages are redacted before being posted to Slack/Discord, and rotating log files are now created `0600` instead of world-readable `0644`
  - Unit tests cover both redaction helpers (PAT/installation tokens, embedded-in-output, plain HTTPS, and SSH remotes left untouched)

## [v0.0.117] - 2026-09-14

### Features

- Attribute merged-PR job completion to whoever merged it ([#219](https://github.com/nairiai/nairid/pull/219))
  - When a job completes because its pull request was merged, the completion message now reads `Job complete - Pull request was merged by <name>` instead of the bare `Job complete - Pull request was merged`, so users no longer have to ask the agent who merged the PR
  - The merger is resolved via `gh pr view --json mergedBy` (by stored PR ID when available, otherwise by branch), preferring the GitHub display name and falling back to the login
  - Best-effort by design: the lookup only runs on the terminal `merged` path, and any failure logs a warning and falls back to the plain "was merged" message, so it never blocks job completion. No new GitHub token scope is required — it reuses the same `gh pr view` call already used for PR state

## [v0.0.116] - 2026-09-06

### Bugfixes

- Treat empty OpenCode agent turns as an empty result instead of a hard error ([#218](https://github.com/nairiai/nairid/pull/218))
  - When an OpenCode session finished a turn without emitting a text message, result extraction returned `no text message found`, which nairid surfaced as `failed to extract OpenCode result` and failed the whole session start. Legitimate empty turns (e.g. a turn that only ran tools, or produced no final assistant text) were treated as fatal
  - `extractResult` now treats a turn with no text message as a valid empty result rather than erroring, so the session completes normally instead of aborting
  - Updated unit tests to cover the empty-turn path

## [v0.0.115] - 2026-07-01

### Security

- Clear Trivy HIGH/CRITICAL findings in Go dependencies and toolchain ([#215](https://github.com/nairiai/nairid/pull/215))
  - Bumps `golang.org/x/crypto` `v0.47.0` → `v0.52.0` (9 HIGH, `x/crypto/ssh` family) and `golang.org/x/net` `v0.49.0` → `v0.55.0` (6 HIGH, HTML render / HTTP2 / idna); `x/sys` and `x/text` move transitively. All four are indirect dependencies
  - Bumps the `go` directive `1.24.1` → `1.25.11`, clearing the flagged stdlib CVEs including CRITICAL `CVE-2025-68121` (crypto/tls incorrect certificate validation during TLS session resumption). `1.25.11` is the highest `1.25.x` fix version in the report, so it clears every flagged stdlib CVE
  - Required CI adjustments downstream of the `go` bump: golangci-lint `v1.64.8` → `v2.12.2` (action `v6` → `v8`, first release built with go1.25), CI now reads the Go version from `go.mod` (`go-version-file`) instead of a stale `1.21` pin, and one staticcheck `QF1006` nit is fixed with a behavior-identical loop rewrite
  - Scope: source-level findings only (Go modules + stdlib). Runtime-image OS packages and bundled CLI tooling are handled where that image is built

## [v0.0.114] - 2026-06-15

### Bugfixes

- Extract Claude session id from result event, not first hook event ([#213](https://github.com/nairiai/nairid/pull/213))
  - On `claude --resume <id> -p --output-format stream-json --verbose`, Claude Code emits `SessionStart` hook events FIRST, carrying the CLI's bootstrap session id, BEFORE `switchSession()` swaps in the actual resumed id. `extractSessionID` returned the first non-empty `session_id` in the stream and so persisted that ephemeral hook id. The next `--resume` then failed with `No conversation found with session ID: <ephemeral>` because that id was never written to disk — only the resumed/on-disk id appears in the system init event onward and in the result event
  - When the failed resume returned `exit status 1`, the backend's `ProcessSystemMessage` matched `isAgentErrorMessage` → archived the job → the next user reply created a brand-new conversation seeded only from the visible thread. In-session state (TODO list, scratchpad, anything not echoed to the channel) was silently lost
  - Only fires when a SessionStart hook is configured (project-level `.claude/settings.json`, repo-level checked-in config, or user-level `~/.claude/settings.json` on self-hosted hosts) — which is why the bug was intermittent and only surfaced in real-world setups where Claude Code was also used interactively. Managed agent containers don't configure hooks, so they were silently relying on the first-vs-result session id being equal
  - Fix: `extractSessionID` now walks messages back-to-front and prefers the `result` event's `session_id` (always the post-switch / on-disk id). Falls back to the last non-empty `session_id` if the CLI exits before emitting a result event (defensive — covers mid-stream session switches, malformed result events, and CLI crashes). Pure transform — no concurrency state, works identically for `MAX_CONCURRENCY=1` and worktree mode
  - Added 5 new unit tests including a verbatim replay of the line ordering captured from real `claude 2.1.139 --resume` output with a SessionStart hook configured

## [v0.0.113] - 2026-06-10

### Features

- Make CLI session timeout configurable via `NAIRI_MAX_SESSION_MS` ([#212](https://github.com/nairiai/nairid/pull/212))
  - The per-CLI-session timeout was previously hardcoded at 1 hour in `clients/process.go`. Any single agent turn (plan → implement → test in one run) was force-killed at the 60-minute mark, regardless of how the daemon was deployed — long single-turn workloads required a nairid rebuild to lift the cap
  - Replaces the hardcoded `DefaultSessionTimeout` constant with a `SessionTimeout()` accessor that reads `NAIRI_MAX_SESSION_MS` (with `EKSEC_MAX_SESSION_MS` accepted as a legacy fallback) as an integer number of milliseconds. Invalid or non-positive values fall back to the 1-hour default and emit a warning log so misconfiguration is visible
  - All four CLI client packages (`claude`, `codex`, `opencode`, `cursor`) now read the value once at session start and reuse it for both the context deadline and the timeout reported in error messages and log lines — operators can grep container logs for the exact configured value
  - `NAIRI_MAX_SESSION_MS` and `EKSEC_MAX_SESSION_MS` are added to `BlockedEnvVars` so nairid-internal config does not leak into agent subprocesses. Existing harness env-filtering tests cover the new vars
  - Operational impact: defaults are unchanged (1h cap as before). Operators that want longer single-turn sessions can set e.g. `NAIRI_MAX_SESSION_MS=10800000` (3h) on the container env and restart nairid — no rebuild required

## [v0.0.112] - 2026-05-27

### Bugfixes

- Populate `cost_usd` for Codex platform model ids + fix cross-user OpenCode db lookup ([#211](https://github.com/nairiai/nairid/pull/211))
  - **Codex**: `services/pricing/codex.go` was keyed on OpenAI's public model names (`gpt-5`, `gpt-5-mini`, `gpt-5-codex`) but the Nairi platform exposes Codex models as dotted-minor ids (`gpt-5.4-mini`, `gpt-5.3-codex`, `gpt-5.5`, `gpt-5.5-pro`, `gpt-5.2-base`) and the Codex CLI reports those verbatim. `lookupCodexPricing` returned `(0, false)`, `omitempty` dropped the field, and every Codex assistant message landed with `cost_usd = NULL`. A new `^(gpt-N)\.\d+(-suffix)?$` normalisation step now folds platform ids onto the canonical OpenAI keys (`gpt-5.4-mini` → `gpt-5-mini`, `gpt-5.3-codex` → `gpt-5-codex`, etc.); unknown suffixes (`-pro`, `-base`) fall through to the existing prefix matcher → base-model pricing. Pricing values are unchanged
  - **OpenCode**: `runOpenCodeDB` shelled out to `opencode db --format json` via plain `exec.CommandContext`, so the OpenCode CLI resolved its SQLite path from the *caller's* `$HOME`. In managed-mode containers nairid runs as `ccagent` but the actual `opencode run` subprocess runs as `agentrunner`, so the post-turn lookup read an empty `/home/ccagent/.local/share/opencode/opencode.db` while the real session data lived in `/home/agentrunner/.local/share/opencode/opencode.db`. Result: every OpenCode assistant message went out with NULL cost, tokens, and model. Now routed through `clients.BuildAgentCommandWithContext`, which executes the lookup via `sudo -u agentrunner` with `HOME=/home/agentrunner` injected — the same mechanism the `opencode run` subprocess already goes through, so the two paths always agree on where the SQLite lives. Self-hosted mode stays direct
  - Also added `log.Warn` on every silent-fail branch in `fetchOpenCodeUsage` (empty/`unknown` session id, suspicious id, db query error, JSON parse error, zero rows, no cost & no tokens) so the next regression of this shape is visible in container logs

## [v0.0.111] - 2026-05-27

### Features

- Emit per-message cost on assistant messages ([#210](https://github.com/nairiai/nairid/pull/210))
  - Each CLI harness now extracts cost and token usage after a conversation turn finishes and attaches it to the outgoing `assistant_message_v1` payload, so the backend can persist `cost_usd`, `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_write_tokens`, and `model` on every `conversation_messages` row
  - Per-harness behaviour:
    - **Claude**: `total_cost_usd` comes from the final `result` event; tokens are taken from the result's `usage` rollup when present, else summed across per-`assistant`-event usage blocks
    - **Codex**: input / cached / output tokens summed across `turn.completed` events; cost is computed via a local pricing table in `services/pricing/codex.go` keyed by model id (kept in nairid so pricing updates ship with nairid and the backend stays a dumb persistence layer)
    - **OpenCode**: shells out to `opencode db --format json` after the subprocess exits to query the local SQLite database for assistant messages created during this run, then sums cost and tokens. Best-effort: on any failure the message is sent with NULL cost columns and the turn does not fail
    - **Cursor**: emits nothing; leaves all cost fields nil
  - Wire change is backward compatible — all new cost fields are `omitempty`, so a backend without the matching schema columns will ignore them. Depends on `presmihaylov/claudecontrol#938` which adds the columns and API surface
  - Pricing-table caveat: the Codex pricing in `services/pricing/codex.go` reflects public OpenAI pricing as of 2026-05; future price changes require a nairid release

## [v0.0.110] - 2026-05-19

### Bugfixes

- Prune stale refs on git fetch to recover from D/F conflicts ([#209](https://github.com/nairiai/nairid/pull/209))
  - `FetchOrigin` now runs `git fetch --prune origin` instead of plain `git fetch origin`, so any stale `refs/remotes/origin/*` whose upstream branch has been deleted are removed automatically during the fetch
  - Fixes a class of failure where renaming a branch upstream from `foo` to `foo/bar/baz` left the old `foo` ref on disk as a loose file, blocking creation of the new nested ref with a classic git directory/file (D/F) conflict (`unable to update local ref`). The error surfaced inside the worktree pool replenisher and inside synchronous `prepare Git environment` paths, which meant **every new conversation on the affected agent failed** until someone manually ran `git remote prune origin` inside the container
  - Added a regression test `TestFetchOrigin_PrunesStaleRefsOnDFConflict` that wires up a bare upstream, simulates the exact rename pattern that produced the production failure, and verifies recovery. The test fails (with the same production error message) when the fix is reverted
  - Operational impact: containers running pre-0.0.110 binaries that already have a stale leaf ref colliding with a new nested upstream branch will continue to require a one-time `git remote prune origin` inside the affected repo. Once on 0.0.110, the prune happens automatically on every fetch and the failure cannot recur

## [v0.0.109] - 2026-05-12

### Bugfixes

- Clean skills directory when zero skills are enabled ([#207](https://github.com/nairiai/nairid/pull/207))
  - `ProcessSkills` returned early when no skill files were present, **before** wiping the agent-specific skills directory (e.g. `~/.claude/skills`)
  - As a result, disabling the last skill on an agent left the previously extracted skills on disk and the agent kept loading them
  - Moved the empty check to **after** the cleanup so the directory is always reset to a fresh, empty state regardless of how many skills are enabled. Same fix applied to the Claude Code, OpenCode, and Codex skill processors
  - Partial unchecks (e.g. 5 → 4) already worked; this only affected the N → 0 case
  - Added regression tests for all three processors that seed a stale skill and assert it is removed when no skills are enabled

## [v0.0.108] - 2026-05-12

### Bugfixes

- Update default CLI models for codex and opencode ([#206](https://github.com/nairiai/nairid/pull/206))
  - The hardcoded daemon defaults pointed at models that no longer work in production
  - Codex defaulted to `gpt-5`, which is not supported under ChatGPT-account OAuth (the auth mode used by production deployments) and fails with HTTP 400: `"The 'gpt-5' model is not supported when using Codex with a ChatGPT account."`
  - OpenCode defaulted to `opencode/grok-code`, which is not in the OpenCode Zen catalog anymore
  - Replaced with verified-working defaults: codex → `gpt-5.5`, opencode → `opencode/claude-sonnet-4-6`
  - Inline comments in `validateModelForAgent` and README examples were updated to match
  - Scope: only affects containers that run `nairid` without an explicit `--model` flag — containers that already receive `--model` from `deploy.sh` are unchanged. Claude/cursor defaults are untouched

## [v0.0.107] - 2026-05-09

### Bugfixes

- Drop orphan worktrees when git refuses, accept `job_` prefix ([#205](https://github.com/nairiai/nairid/pull/205))
  - `CleanupOrphanedWorktrees` now falls back to `os.RemoveAll` when `git worktree remove --force` fails (e.g. when the bare repo's `.git/worktrees/` registry has been wiped or the worktree was never registered). Previously the function logged a warning and left the directory on disk; on every periodic cleanup tick the same orphan was re-discovered and re-skipped, causing tens of GB of orphan worktree directories to accumulate on long-lived agent containers
  - `CleanupStaleJobWorktrees` now scans both the legacy `j_*` and the current `job_*` prefixes. Without `job_*` support, broken worktrees on the post-rename naming convention were silently skipped
  - Adds three regression tests covering the fallback path, the kept-tracked path, and the new prefix support

## [v0.0.106] - 2026-05-04

### Bugfixes

- Pipe agent prompt via stdin on Windows to avoid `cmd.exe` truncation ([#204](https://github.com/nairiai/nairid/pull/204))
  - Fixes a Windows-only bug where Slack/Discord messages reached the Claude agent as empty content because `cmd.exe`'s batch-line interpreter truncated the `-p` argument at the first embedded newline
  - The bug surfaced on every Slack/Discord message (the sender header `[Sender: ...]\n\n<msg>` made the prompt multi-line) and on any user message containing newlines (Shift+Enter, code blocks, multi-line questions)
  - Introduces a build-tag-isolated `clients.ApplyPrompt` helper: on Unix, behaviour is byte-equivalent to before; on Windows, the prompt is delivered via `cmd.Stdin`, bypassing `cmd.exe`'s argument parsing entirely
  - Linux and macOS binaries are unchanged in this code path

## [v0.0.105] - 2026-04-27

### Features

- Per-agent namespace isolation for worktrees, state, and logs ([#203](https://github.com/nairiai/nairid/pull/203))
  - Multiple nairid instances on the same machine no longer corrupt each other's worktrees or state files
  - Each instance gets a unique namespace derived from `NAIRI_AGENT_ID`, `EKSEC_AGENT_ID`, or the sanitized repo identifier
  - Worktrees are scoped under `.eksec_worktrees/<namespace>/j_...`
  - State files are scoped under `.config/eksecd/instances/<namespace>/state.json`
  - Logs are scoped under `.config/eksecd/instances/<namespace>/logs/`
  - Directory lock prevents two instances with the same namespace from running concurrently
  - Legacy state.json is auto-migrated to the namespaced path on first upgrade
  - Extracted `SanitizeNamespace`, `ResolveInstanceNamespace`, `NamespacedInstanceDir`, `NamespacedStatePath`, `NamespacedLogsDir`, and `MigrateLegacyState` into focused, unit-tested functions

## [v0.0.104] - 2026-03-22

### Bugfixes

- Surface OpenCode API errors in error messages ([#200](https://github.com/nairiai/nairid/pull/200))
  - OpenCode API errors (e.g. authentication failures, rate limits) are now properly surfaced in agent error messages instead of being silently swallowed
  - Adds test coverage for error message extraction from OpenCode responses

## [v0.0.103] - 2026-03-20

### Bugfixes

- Kill entire process tree on timeout to prevent worker pool exhaustion ([#199](https://github.com/nairiai/nairid/pull/199))
  - When a CLI process times out, the entire process tree is now killed (not just the parent), preventing orphaned child processes from consuming worker pool slots
  - Adds process group management using `setpgid` to ensure all child processes are grouped and terminated together
  - Splits platform-specific process management into build-tagged files for proper Windows cross-compilation

## [v0.0.102] - 2026-03-17

### Bugfixes

- Fall back to WebSocket when HTTP message delivery fails ([#197](https://github.com/nairiai/nairid/pull/197))
  - When Cloudflare WAF or network issues block HTTP POST to the backend, messages are now delivered via WebSocket instead of being permanently lost
  - Introduced testable interfaces for HTTP and WebSocket message delivery

## [v0.0.101] - 2026-03-17

### Bugfixes

- Skip macOS metadata entries in skill ZIP extraction ([#196](https://github.com/nairiai/nairid/pull/196))
  - ZIP files created on macOS include `__MACOSX/` resource fork directories that broke single root directory detection
  - This caused skills to be double-nested, making them invisible to the agent
  - Also skip `.DS_Store` files during extraction

## [v0.0.100] - 2026-03-11

### Bugfixes

- Upgrade user_message to start_conversation when job not found locally ([#195](https://github.com/nairiai/nairid/pull/195))

### Documentation

- Rebrand README from eksec to Nairi

## [v0.0.99] - 2026-03-08

### Changes

- Rename binary from eksecd to nairid in build scripts and Makefile
- Remove truncation from progress payloads ([#194](https://github.com/nairiai/nairid/pull/194))
- Update README clone URL to nairid ([#193](https://github.com/nairiai/nairid/pull/193))

## [v0.0.98] - 2026-03-07

### Bugfixes

- Prevent message replay on agent restart ([#192](https://github.com/nairiai/eksecd/pull/192))
  - Move message acking from dispatch-time to after-processing for crash safety
  - Add RemoveQueuedMessagesForJob to clean up queued messages after job completion
  - Skip already-seen messages in the HTTP poller via dispatcher's IsMessageSeen check
  - Clean up orphaned and completed/failed job messages during recovery instead of replaying them
  - Stop persisting poller messages to local queue to prevent stale state.json entries

## [v0.0.97] - 2026-03-06

### Features

- Add real-time agent progress streaming ([#189](https://github.com/nairiai/eksecd/pull/189))
  - Stream intermediary progress events (tool calls, text output, reasoning, subagent activity) from CLI agents to the backend in real-time
  - Switch CLI clients from CombinedOutput to StdoutPipe with line reader for real-time NDJSON streaming
  - Add per-agent progress mappers normalizing CLI-specific events into a uniform AgentProgressPayload format
  - Add non-blocking progress queue in MessageSender with best-effort HTTP delivery

## [v0.0.96] - 2026-03-06

### Bugfixes

- Prevent goroutine leak in MessagePoller and MessageSender on WS reconnect ([#190](https://github.com/nairiai/eksecd/pull/190))
  - Stop old poller and sender goroutines before starting new ones when the WebSocket connection is re-established
  - Prevents unbounded goroutine growth during repeated reconnections

- Make outbound attachments dir group-writable for agentrunner ([#191](https://github.com/nairiai/eksecd/pull/191))
  - Set group-writable permissions on the outbound attachments directory so the agentrunner user can write files to it
  - Fixes attachment delivery failure when running as the unprivileged agentrunner user

## [v0.0.95] - 2026-03-06

### Features

- Add outbound attachment support for agent responses ([#188](https://github.com/nairiai/eksecd/pull/188))
  - Allow agents to send file attachments (images, documents) back to users alongside text responses
  - Files placed in the outbound attachments directory are automatically uploaded and delivered

### Improvements

- Persist dedup state to disk and increase TTL to 60min ([#187](https://github.com/nairiai/eksecd/pull/187))
  - Save message deduplication state to disk so it survives agent restarts
  - Increase dedup TTL from previous value to 60 minutes for better duplicate rejection

## [v0.0.94] - 2026-03-06

### Features

- Add HTTP message redundancy for agent communication ([#184](https://github.com/nairiai/eksecd/pull/184))
  - Add HTTP-based message sending alongside WebSocket for improved reliability
  - Messages are sent via both channels with deduplication on the receiving end
  - Provides fallback communication path when WebSocket connections are unstable

### Bugfixes

- Fix PR footer branding: nairid -> Nairi ([#186](https://github.com/nairiai/eksecd/pull/186))
  - Update PR description footer to use correct Nairi branding instead of old nairid name

## [v0.0.93] - 2026-03-05

### Bugfixes

- Preserve execute permissions when extracting skill ZIP files ([#185](https://github.com/nairiai/eksecd/pull/185))
  - Read file mode from ZIP entry instead of hardcoding 0644, preserving the execute bit for shell scripts
  - Add explicit chmod via sudo after tee write when permissions differ from default
  - Prevents agents from wasting a turn running `chmod +x` on freshly provisioned skill scripts

## [v0.0.92] - 2026-03-05

### Features

- Support configurable system prompt from backend ([#181](https://github.com/nairiai/eksecd/pull/181))
  - Accept optional `system_prompt` field in start conversation payload from the backend
  - When provided, use it as the base prompt and append repo context and mode instructions dynamically
  - Fall back to built-in default prompts when no custom prompt is specified

### Bugfixes

- Fix startup crash loop when GitHub token in git remote URL has expired ([#182](https://github.com/nairiai/eksecd/pull/182))
  - Apply latest GitHub token to git remote URL before validation on startup, preventing crash loops when the token baked into the URL from a previous session has expired

- Fix index.lock race in concurrent worktree creation ([#183](https://github.com/nairiai/eksecd/pull/183))
  - Add `mainRepoMu` mutex to serialize git operations (checkout, reset, clean) on the main repository
  - Prevent `.git/index.lock` conflicts between the pool replenisher and synchronous worktree creation

## [v0.0.91] - 2026-03-05

### Bugfixes

- Revert infrastructure-facing renames for backwards compat ([#180](https://github.com/nairiai/eksecd/pull/180))
  - Restore original `eksecd` binary name, environment variable prefixes, and branch naming to maintain backwards compatibility with existing infrastructure tooling and deployment scripts

## [v0.0.90] - 2026-03-05

### Refactoring

- Rename eksec/eksecd to nairi/nairid across codebase ([#178](https://github.com/nairiai/eksecd/pull/178))
  - Rename Go module from `eksecd` to `nairid`
  - Rename environment variables with `NAIRI_` prefix (backwards-compatible fallback to `EKSEC_` prefix)
  - Update default WebSocket API URL to `api.nairi.ai`
  - Update API key prefix validation to accept `nairid_` alongside legacy `eksecd_` and `ccagent_` prefixes
  - Update branch naming prefix from `eksecd/` to `nairid/`
  - Add backwards compatibility for `eksecd/` branch prefix in cleanup and pool reclaim functions
  - Update all system prompts and branding references

## [v0.0.89] - 2026-03-05

### Features

- Consume user metadata from restructured message payloads ([#179](https://github.com/nairiai/eksecd/pull/179))
  - Parse `sender_metadata` (name, email, platform) from incoming messages and prepend a `[Sender: ...]` header so agents can identify who sent each message in multi-user scenarios
  - Add multi-user context instructions to Slack system prompts
  - Clean Slack mrkdwn email formatting automatically

## [v0.0.88] - 2026-02-27

### Bugfixes

- Evict idle jobs from dispatcher to free worker pool slots ([#175](https://github.com/nairiai/eksecd/pull/175))
  - Detect and evict jobs that become idle while waiting in the dispatcher queue, preventing worker pool exhaustion when jobs are abandoned before being assigned a worker

## [v0.0.87] - 2026-02-22

### Improvements

- Improve PR title generation robustness ([#174](https://github.com/eksecai/eksecd/pull/174))
  - Rewrite PR title generation and update prompts with explicit good/bad examples and strict output constraints
  - Add `SanitizePRTitle()` post-processing function to strip markdown headers, bullets, quotes, preamble phrases, and leaked body content
  - Apply sanitization in all four title generation paths (create/update, regular/worktree)
  - Add comprehensive test coverage for title sanitization (21 test cases)

## [v0.0.86] - 2026-02-21

### Reverts

- Revert "Use remote MCP server URLs directly" ([#173](https://github.com/eksecai/eksecd/pull/173))
  - Reverts direct remote URL passthrough for MCP servers using SSE transport
  - MCP servers will again go through the HTTP POST proxy instead of connecting directly

## [v0.0.84] - 2026-02-18

### Bugfixes

- Fix OpenCode parser failing on first-run database migration ([#172](https://github.com/eksecai/eksecd/pull/172))
  - Handle case where OpenCode's sqlite database doesn't exist yet on first run, preventing parser initialization failures


## [v0.0.83] - 2026-02-18

### Features

- Add native Codex artifact provisioning support ([#171](https://github.com/eksecai/eksecd/pull/171))
  - Implement `CodexRulesProcessor` that builds `~/.codex/AGENTS.md` from rule files
  - Implement `CodexMCPProcessor` that writes `~/.codex/config.toml` in TOML format with `shell_environment_policy` set to never filter env vars
  - Implement `CodexProxiedMCPProcessor` for proxy-mode MCP config for Codex
  - Implement `CodexSkillsProcessor` that extracts skills to `~/.codex/skills/`
  - Add `github.com/pelletier/go-toml/v2` dependency for TOML serialization

## [v0.0.82] - 2026-02-15

### Bugfixes

- Fix env vars not applied in managed containers ([#169](https://github.com/eksecai/eksecd/pull/169))
  - Remove `IsSelfHosted()` guard from `fetchAndApplyEnvVars` so environment variables configured via the platform are fetched and applied to all agents, not just self-hosted ones.

## [v0.0.81] - 2026-02-15

### Features

- Fetch and apply container env vars on startup ([#168](https://github.com/eksecai/eksecd/pull/168))
  - On startup, self-hosted agents now fetch environment variables from the API (`/api/agents/env`) and apply them via EnvManager before artifact fetching.
  - Managed installations skip this step. Enables configuring agent environment variables through the platform without modifying container config.

## [v0.0.80] - 2026-02-15

### Bugfixes

- Fix divergent branches by resetting to origin ref ([#164](https://github.com/eksecai/eksecd/pull/164))
  - Replace `git pull --rebase` with `git fetch` + `git reset --hard origin/<branch>` to handle divergent branches caused by force pushes or rebases, preventing exit status 128 errors.
- Fix opencode text messages joined without newlines ([#167](https://github.com/eksecai/eksecd/pull/167))
  - Insert newline separators between consecutive text messages in OpenCode response parsing so multi-part responses don't get concatenated into a single unreadable block.

## [v0.0.79] - 2026-02-14

### Bugfixes

- Fix MCP proxy unreachable through secret proxy ([#166](https://github.com/eksecai/eksecd/pull/166))
  - Add `NO_PROXY`/`no_proxy` with MCP proxy hostname to agent process environment so MCP traffic bypasses the secret proxy and connects directly.
  - The secret proxy cannot resolve `mcp-proxy.internal` (only in eksecd container's `/etc/hosts`), causing 502 errors when MCP requests were routed through it.

## [v0.0.78] - 2026-02-14

### Features

- Add MCP proxy support via AGENT_MCP_PROXY ([#165](https://github.com/eksecai/eksecd/pull/165))
  - When `AGENT_MCP_PROXY` is set, eksecd fetches MCP server configs from the proxy's `/servers` endpoint and writes HTTP-based MCP URLs into agent config files instead of spawning local stdio MCP server processes.
  - Supports both Claude Code (`type: "http"` in `.claude.json`) and OpenCode (`type: "remote"` in `opencode.json`) agent types.
  - Skip MCP config artifact downloads when proxy is enabled since the proxy handles server lifecycle.
  - Update default backend URL to api.eksec.ai.

## [v0.0.77] - 2026-02-13

### Bugfixes

- Fix git pull failing on divergent branches ([#163](https://github.com/eksecai/eksecd/pull/163))
  - Add `--rebase` flag to `git pull` in both `PullLatest()` and `PullLatestInWorktree()` to handle divergent branches gracefully instead of failing with exit status 128.

## [v0.0.76] - 2026-02-10

### Bugfixes

- Reduce GitHub API calls in idle job checker ([#162](https://github.com/eksecai/eksecd/pull/162))
  - Remove `HasExistingPR` guard from `CheckPRStatus`: call `GetPRState` directly and treat "no pull requests found" as `no_pr`. Saves 1 API call per job per idle check cycle.
  - Cache "no PR" result: store `PullRequestID="none"` in JobData so future idle checks skip GitHub API entirely for jobs without PRs.
  - Eliminates ~280 API calls/minute for orgs with many tracked jobs across containers.

## [v0.0.75] - 2026-02-10

### Bugfixes

- Fix rate limit retry in worktree operations and reduce redundant GitHub API calls
  - Add rate limit backoff extension (10min) to `executeWithRetryInDir`, matching `executeWithRetry` behavior. Previously worktree PR operations only retried for 2 minutes on rate limit errors.
  - Skip redundant `HasExistingPR` and `GetCurrentBranch` API calls in PR footer validation when the caller already knows a PR exists. Saves 2-3 GitHub API calls per job.

## [v0.0.74] - 2026-02-10

### Bugfixes

- Extend retry backoff to 10 minutes for GitHub API rate limit errors
  - Network errors (timeout, dial tcp) keep the existing 2-minute retry window
  - Rate limit errors now dynamically extend backoff to MaxInterval=60s, MaxElapsedTime=10min on first detection
  - Fixes jobs still failing after v0.0.73 because 2-minute window was too short for GitHub rate limit resets

## [v0.0.73] - 2026-02-10

### Bugfixes

- Handle GitHub API rate limit errors with retry backoff ([#161](https://github.com/eksecai/eksecd/pull/161))
  - Add rate limit error patterns (`rate limit`, `secondary rate limit`, `abuse detection`) to `isRecoverableGHError()`
  - Previously, rate limit errors were treated as permanent failures; now they trigger exponential backoff retry (2s→30s over 2min)

## [v0.0.72] - 2026-02-10

### Bugfixes

- Fix PR footer detection to prevent duplicate footers ([#160](https://github.com/eksecai/eksecd/pull/160))

## [v0.0.71] - 2026-02-10

### Bugfixes

- Fix job failure when pool worktree becomes invalid after container restart ([#155](https://github.com/eksecai/eksecd/pull/155))
  - Add `WorktreeExists()` validation in `Acquire()` before attempting `git worktree move`
  - After container restarts, pool directories persist on the volume but `.git/worktrees/` entries are lost, causing `git worktree move` to fail with "is not a working tree"
  - New `ErrWorktreeInvalid` sentinel error allows graceful fallback to synchronous worktree creation instead of hard-failing the job
  - Fix `cleanupFailedWorktree` to fall back to `os.RemoveAll` when `git worktree remove` fails on unregistered worktrees

## [v0.0.70] - 2026-02-09

### Bugfixes

- Fix file edits being reverted on session exit in self-hosted mode ([#154](https://github.com/eksecai/eksecd/pull/154))
  - Use `--dangerously-skip-permissions` instead of `--permission-mode bypassPermissions` for Claude CLI
  - The latter caused Claude Code to revert all file edits when the process exited, resulting in empty worktrees and no PRs

## [v0.0.69] - 2026-02-09

### Bugfixes

- Fix startup deadlock: close initialFillDone channel after worktree pool fill ([#153](https://github.com/eksecai/eksecd/pull/153))
  - The WaitForInitialFill() channel was never signaled, causing the main thread to block indefinitely
  - Prevented WebSocket connection, job recovery, and all post-startup initialization
- Fix flaky TestMultipleJobsProcessInParallel test ([#153](https://github.com/eksecai/eksecd/pull/153))
  - Race condition where cleanup could close channel before test sent final messages

## [v0.0.68] - 2026-02-09

### Bugfixes

- Fix git index.lock race condition between worktree pool fill and job recovery on startup ([#152](https://github.com/eksecai/eksecd/pull/152))
  - Added WaitForInitialFill() to serialize pool initialization before job recovery
  - Prevents concurrent git reset --hard operations competing for .git/index.lock

## [v0.0.67] - 2026-02-08

### Changes

- Update and trim system prompt copy ([#151](https://github.com/eksecai/eksecd/pull/151))

## [v0.0.66] - 2026-02-08

### Changes

- Replace OpenCode rules with AGENTS.md approach ([#149](https://github.com/eksecai/eksecd/pull/149))
  - OpenCode now reads rules from a single `~/.config/opencode/AGENTS.md` file instead of `opencode.json` with glob patterns
  - Rules are rendered with `# Rules` header, `## <title>` sections with descriptions and body content
  - Front matter (title, description) is extracted and stripped from rule files
  - Adds `ReadRuleBody()` function that returns title, description, and body
  - Cleans up old `opencode.json` and `rules/` directory artifacts from previous approach

### Bugfixes

- Fix: recover valid responses when CLI crashes without result event ([#150](https://github.com/eksecai/eksecd/pull/150))
  - Recovers valid Claude responses when CLI exits non-zero but produced valid assistant messages without a final result JSON event
  - Prevents intermittent "failed to continue Claude session" errors where valid responses were being discarded

## [v0.0.65] - 2026-02-07

### Changes

- Remove all token refresh logic from ccagent ([#147](https://github.com/eksecai/eksecd/pull/147))
  - Backend is now the sole OAuth token refresh authority
  - Removes `RefreshToken()` API client method, `startTokenMonitoringRoutine()`, `handleRefreshToken()` handler
  - Removes `MessageTypeRefreshToken` constant and `RefreshTokenPayload`
  - Renames `FetchAndRefreshAgentTokens()` to `FetchAndSetAgentToken()` then removes it as dead code
  - Eliminates race condition where multiple components independently refreshed tokens

### Bugfixes

- Fix permission bug causing container crash loops ([#148](https://github.com/eksecai/eksecd/pull/148))

## [v0.0.60] - 2026-02-04

### Bugfixes

- Migrate from deprecated socket.io client libraries to monorepo ([#141](https://github.com/eksecai/eksecd/pull/141))
  - Fixes critical race condition panic: `sync/atomic: store of inconsistently typed value into Value`
  - Migrates from `engine.io-client-go@v1.1.0` and `socket.io-client-go@v1.1.0` to unified monorepo packages
  - The monorepo uses type-safe generic atomic types preventing this class of bugs at compile time
  - Related issue: https://github.com/zishang520/engine.io-client-go/issues/14

## [v0.0.59] - 2026-02-04

### Bugfixes

- Fix worktree cross-pollination by resetting main repo before creation ([#140](https://github.com/eksecai/eksecd/pull/140))
  - Prevents newly created worktrees from picking up changes from other branches
  - Resets main repository to default branch before worktree creation
  - Applies to both synchronous worktree creation and worktree pool replenishment
  - Properly propagates errors instead of silently continuing on failure

- Cleanup existing worktree when duplicate jobID received ([#139](https://github.com/eksecai/eksecd/pull/139))
  - Handles cases where start_conversation is sent multiple times for the same job
  - Cleans up existing worktree before creating a new one for the same jobID
  - Prevents resource leaks from duplicate job handling

## [v0.0.58] - 2026-02-03

### Bugfixes

- Fix binary compatibility with Alpine/musl-based containers
  - Build release binaries with CGO_ENABLED=0 for static linking
  - Fixes "required file not found" errors on Alpine Linux
  - Ensures eksecd works in Docker containers using musl libc

## [v0.0.57] - 2026-02-03

### Bugfixes

- Processor exits on job failure to prevent worker pool exhaustion ([#136](https://github.com/eksecai/eksecd/pull/136))
  - Fixes issue where failed jobs could exhaust the worker pool
  - Ensures processor properly exits on job failure
  - Improves system stability and resource management

## [v0.0.56] - 2026-02-01

### Features

- Add message deduplication to JobDispatcher ([#135](https://github.com/eksecai/eksecd/pull/135))
  - Prevents duplicate messages from being processed multiple times
  - Tracks seen message IDs with a 5-minute TTL for deduplication
  - Automatic cleanup of expired entries to prevent memory leaks
  - Improves reliability in scenarios with message retransmissions

## [v0.0.55] - 2026-02-01

### Bugfixes

- Fix opencode.json permission denied for agentrunner user ([#134](https://github.com/eksecai/eksecd/pull/134))
  - Fixes file permission issue when writing opencode.json and rules in process isolation mode
  - Ensures configuration files are written to the correct user's home directory

## [v0.0.54] - 2026-02-01

### Bugfixes

- Fix bug with parallel work trees ([#133](https://github.com/eksecai/eksecd/pull/133))
  - Adds a dispatcher layer for proper job-to-worktree routing
  - Fixes race conditions when multiple jobs run concurrently across worktrees

## [v0.0.53] - 2026-02-01

### Refactoring

- Rebrand to eksec in all prompts ([#126](https://github.com/eksecai/eksecd/pull/126))
  - Updates all user-facing prompt text from ccagent to eksec branding
- Rename ccagent to eksec across codebase ([#127](https://github.com/eksecai/eksecd/pull/127))
  - Renames binary from ccagent to eksecd
  - Updates config directory from ~/.config/ccagent/ to ~/.config/eksecd/
  - Changes environment variables from CCAGENT_* to EKSEC_* prefix
  - Updates all internal references and documentation
- Rename eksec to eksecd across codebase ([#128](https://github.com/eksecai/eksecd/pull/128))
  - Finalizes binary and package naming to eksecd
  - Ensures consistent naming across all code paths

## [v0.0.52] - 2026-01-31

### Bugfixes

- Add 1-hour timeout to all CLI agent session executions ([#124](https://github.com/presmihaylov/eksecd/pull/124))
  - Enforces a 1-hour timeout on all CLI agent sessions (Claude, Codex, Cursor, OpenCode)
  - Prevents runaway sessions from blocking agent resources indefinitely
  - Moves timeout responsibility from the generic process layer to each client implementation
  - Improves system reliability in production multi-agent deployments

## [v0.0.51] - 2026-01-31

### Features

- Add worktree pool for fast job assignment ([#104](https://github.com/presmihaylov/eksecd/pull/104))
  - Pre-creates a pool of git worktrees for instant job assignment
  - Eliminates worktree creation latency when new jobs arrive
  - Manages worktree lifecycle with automatic cleanup and replenishment
  - Includes comprehensive test coverage for pool operations

### Bugfixes

- Replace bufio.Scanner with bufio.Reader to eliminate token-too-long errors ([#107](https://github.com/presmihaylov/eksecd/pull/107))
  - Switches from Scanner to Reader for Claude output parsing
  - Removes the 64KB line length limitation that caused token-too-long errors
  - Simplifies message parsing logic with cleaner implementation
  - Improves reliability when handling large tool results

## [v0.0.50] - 2026-01-27

### Features

- Add X-AGENT-ID header to artifacts API calls ([#106](https://github.com/presmihaylov/eksecd/pull/106))
  - Includes agent identification in artifact API requests
  - Enables server-side tracking of which agent uploaded artifacts
  - Improves observability for multi-agent deployments

### Bugfixes

- Fix OpenCode directory permissions for agentrunner user ([#105](https://github.com/presmihaylov/eksecd/pull/105))
  - Fixes MCP config directory creation with proper ownership for non-root users
  - Ensures permissions and rules processors use correct user paths
  - Resolves directory permission errors in managed execution mode

## [v0.0.48] - 2026-01-24

### Features

- Add concurrent job processing with git worktrees ([#83](https://github.com/presmihaylov/eksecd/pull/83))
  - Enables agents to process multiple jobs simultaneously using isolated git worktrees
  - Each concurrent job runs in its own worktree with separate branches
  - Improves throughput for repositories with multiple pending tasks
  - Maintains isolation between concurrent job executions

## [v0.0.47] - 2026-01-21

### Bugfixes

- Fix parsing failure for large MCP tool results ([#82](https://github.com/presmihaylov/eksecd/pull/82))
  - Adds handler for large `text` fields in `tool_use_result` arrays
  - MCP tools (like `mcp__postgres__query`) return results in a different format than regular tools
  - Truncates text fields over 100KB to prevent bufio.Scanner "token too long" errors
  - Fixes parsing failures when postgres queries return large result sets (100MB+)

## [v0.0.46] - 2026-01-21

### Bugfixes

- Set umask 002 when spawning agent processes in managed mode ([#81](https://github.com/presmihaylov/eksecd/pull/81))
  - Wraps agent commands in bash with umask 002 for group-writable file permissions
  - Enables eksecd to delete files created by agent during git clean operations
  - Fixes "Permission denied" errors on git operations with agentrunner-created files

## [v0.0.45] - 2026-01-20

### Bugfixes

- Fix: write .claude.json as target user via sudo ([#80](https://github.com/presmihaylov/eksecd/pull/80))
  - Writes .claude.json configuration file with proper ownership when running as non-root
  - Uses sudo to ensure file is created with target user permissions
  - Fixes permission issues when deploying MCP server configurations

## [v0.0.44] - 2026-01-20

### Bugfixes

- Fix deploy artifacts to agent user's home directory ([#79](https://github.com/presmihaylov/eksecd/pull/79))
  - Deploys MCP servers, rules, permissions, and skills to the agent user's home directory
  - Ensures proper file ownership and permissions for agent processes
  - Improves reliability when running agents as non-root users

## [v0.0.43] - 2026-01-20

### Features

- Add process isolation support for agent execution ([#77](https://github.com/presmihaylov/eksecd/pull/77))
  - Enables process isolation for running multiple agent instances
  - Provides better resource isolation and security boundaries
  - Supports isolated execution environments for agent processes
  - Adds comprehensive test coverage for process isolation

### Bugfixes

- Fix extractSessionID to handle non-JSON output before session data ([#76](https://github.com/presmihaylov/eksecd/pull/76))
  - Properly handles Claude Code output that contains non-JSON content before session data
  - Improves parsing reliability when output includes warnings or other text
  - Adds test coverage for edge cases in session ID extraction

## [v0.0.42] - 2026-01-16

### Bugfixes

- Fix checkout remote branch on container redeploy ([#73](https://github.com/presmihaylov/eksecd/pull/73))
  - Properly checks out the remote branch when containers are redeployed
  - Ensures agents start on the correct branch after container restart
  - Improves reliability for container orchestration workflows

- Fix parsing failure for large tool_result outputs ([#75](https://github.com/presmihaylov/eksecd/pull/75))
  - Resolves parsing issues when tool results contain very large outputs
  - Improves handling of buffer sizes for tool result processing
  - Enhances stability for operations with verbose tool outputs

## [v0.0.41] - 2026-01-11

### Bugfixes

- Increase buffer size to 4MB for handling large tool outputs
  - Fixes issues with processing large responses from Claude Code
  - Prevents buffer overflow errors during heavy tool usage
  - Improves reliability for complex operations with verbose output

- Handle detached HEAD state in GetCurrentBranch ([#72](https://github.com/presmihaylov/eksecd/pull/72))
  - Properly handles repositories in detached HEAD state
  - Prevents errors when working with specific commits instead of branches
  - Improves robustness of branch detection logic

## [v0.0.40] - 2026-01-08

### Features

- Add --repo flag to decouple repo from PWD ([#71](https://github.com/presmihaylov/eksecd/pull/71))
  - Enables specifying repository path via --repo flag
  - Decouples repository location from current working directory
  - Improves flexibility for running agents from any directory
  - Useful for scripts and automation that manage multiple repositories

## [v0.0.39] - 2026-01-07

### Features

- Add X-AGENT-ID header with environment variable support ([#70](https://github.com/presmihaylov/eksecd/pull/70))
  - Adds X-AGENT-ID header to API requests for agent identification
  - Supports EKSEC_AGENT_ID environment variable for custom agent IDs
  - Improves agent tracing and debugging capabilities

### Bugfixes

- Extract results from tool_use messages when no text response ([#68](https://github.com/presmihaylov/eksecd/pull/68))
  - Fixes handling of API responses that contain only tool_use blocks
  - Properly extracts results from tool_use message content
  - Improves reliability of agent response processing

- Simplify PR title prompts for smaller model compatibility ([#69](https://github.com/presmihaylov/eksecd/pull/69))
  - Streamlines PR title generation prompts for better compatibility
  - Improves support for smaller language models
  - Reduces prompt complexity while maintaining quality

- Handle empty repository on startup gracefully ([#67](https://github.com/presmihaylov/eksecd/pull/67))
  - Fixes crash when starting agent on empty repositories
  - Adds graceful handling of repositories without commits
  - Improves agent startup reliability

## [v0.0.38] - 2026-01-01

### Features

- Add permissions processor to enable yolo mode for OpenCode ([#66](https://github.com/presmihaylov/eksecd/pull/66))
  - Adds new permissions processor to enable yolo mode for OpenCode client
  - Allows OpenCode agents to run with fewer confirmation prompts
  - Improves agent autonomy and workflow efficiency

## [v0.0.37] - 2026-01-01

### Features

- Add skills support for coding agents ([#65](https://github.com/presmihaylov/eksecd/pull/65))
  - Enables skills loading from repository configuration
  - Supports custom skill definitions for enhanced agent capabilities
  - Allows agents to utilize specialized skills during conversations
  - Improves agent flexibility and extensibility for various use cases

## [v0.0.36] - 2025-12-28

### Bugfixes

- Transform MCP configs for OpenCode compatibility ([#64](https://github.com/presmihaylov/eksecd/pull/64))
  - Fixes MCP server configuration handling for OpenCode client
  - Transforms MCP config format to be compatible with OpenCode
  - Ensures proper MCP server integration across both Claude Code and OpenCode clients

## [v0.0.35] - 2025-12-28

### Features

- Add MCP server configuration support ([#63](https://github.com/presmihaylov/eksecd/pull/63))
  - Enables configuration of MCP (Model Context Protocol) servers for agents
  - Supports defining custom MCP servers in repository configuration
  - Allows agents to interact with external tools and data sources via MCP
  - Includes comprehensive test coverage for MCP processor

## [v0.0.34] - 2025-12-28

### Improvements

- Store Claude Code rules in ~/.claude/rules ([#62](https://github.com/presmihaylov/eksecd/pull/62))
  - Moves rule storage location to ~/.claude/rules directory
  - Aligns with Claude Code's standard rules location
  - Improves compatibility with Claude Code's rules management

## [v0.0.33] - 2025-12-28

### Improvements

- Simplify OpenCode rules and add cleanup ([#61](https://github.com/presmihaylov/eksecd/pull/61))
  - Streamlines OpenCode rules processing for better maintainability
  - Adds cleanup functionality for temporary rule files
  - Improves code organization and reduces complexity

## [v0.0.32] - 2025-12-27

### Features

- Add agent-specific rules processing ([#60](https://github.com/presmihaylov/eksecd/pull/60))
  - Enables processing of agent-specific CLAUDE.md rules from repository
  - Supports custom agent behavior configuration per repository
  - Allows repository owners to define agent-specific instructions and constraints
  - Enhances flexibility for repository-level agent customization

## [v0.0.31] - 2025-12-27

### Features

- Add agent artifacts API support ([#59](https://github.com/presmihaylov/eksecd/pull/59))
  - Enables agents to upload and manage artifacts via API
  - Supports storing and retrieving files generated during agent sessions
  - Provides foundation for artifact sharing between agents and users

### Improvements

- Increase job inactivity timeout to 25h ([#58](https://github.com/presmihaylov/eksecd/pull/58))
  - Extends job inactivity timeout from previous limit to 25 hours
  - Prevents premature job termination for long-running tasks
  - Improves reliability for complex, time-consuming operations

## [v0.0.30] - 2025-12-26

### Features

- Add model flag support for Claude agent ([#57](https://github.com/presmihaylov/eksecd/pull/57))
  - Enables model selection via --model flag for Claude client
  - Allows specifying different Claude models (e.g., claude-sonnet-4-5-20250514)
  - Provides flexibility in choosing model based on task requirements

## [v0.0.29] - 2025-12-25

### Bugfixes

- Handle non-JSON opencode output as raw error ([#56](https://github.com/presmihaylov/eksecd/pull/56))
  - Properly handles error responses from OpenCode that aren't valid JSON
  - Returns raw output as error message for better debugging
  - Improves reliability when working with OpenCode client

## [v0.0.28] - 2025-12-24

### Improvements

- Consolidate model flags into --model ([#53](https://github.com/presmihaylov/eksecd/pull/53))
  - Simplifies CLI by replacing multiple model flags with a single --model flag
  - Improves developer experience with cleaner command syntax
  - Reduces flag complexity for model selection

## [v0.0.27] - 2025-12-24

### Features

- Add support for OpenCode client ([#48](https://github.com/presmihaylov/eksecd/pull/48))
  - Integrates OpenCode as a new supported AI coding client
  - Expands agent compatibility with additional coding assistants
  - Provides seamless integration for OpenCode users

- Add automatic PR title trimming ([#49](https://github.com/presmihaylov/eksecd/pull/49))
  - Automatically trims PR titles that exceed GitHub's character limit
  - Prevents PR creation failures due to overly long titles
  - Improves reliability of automated PR workflows

- Show the correct platform in PR description footer ([#51](https://github.com/presmihaylov/eksecd/pull/51))
  - Displays the actual platform (Slack, Discord) in PR footers
  - Improves traceability of PR origins
  - Enhances multi-platform integration clarity

- Skip token operations for self-hosted ([#52](https://github.com/presmihaylov/eksecd/pull/52))
  - Skips unnecessary token operations in self-hosted deployments
  - Reduces overhead for self-managed installations
  - Improves startup performance for self-hosted agents

### Bugfixes

- Increase API client timeout to 60s ([#50](https://github.com/presmihaylov/eksecd/pull/50))
  - Extends API client timeout from default to 60 seconds
  - Prevents timeout errors during slow API responses
  - Improves reliability for complex operations

## [v0.0.26] - 2025-12-19

### Bugfixes

- Abandon job when remote branch deleted ([#47](https://github.com/presmihaylov/eksecd/pull/47))
  - Automatically detects when a remote branch has been deleted
  - Gracefully abandons jobs that can no longer be completed
  - Prevents agents from getting stuck on deleted branches
  - Improves resource utilization by freeing up workers promptly

## [v0.0.25] - 2025-12-02

### Improvements

- Increase response limit and add context guidelines ([#45](https://github.com/presmihaylov/eksecd/pull/45))
  - Expands response limits for more detailed agent outputs
  - Adds context guidelines for improved response quality
  - Enhances user experience with more comprehensive answers

- Reduce system prompt char limit to 800 ([#46](https://github.com/presmihaylov/eksecd/pull/46))
  - Optimizes system prompt length for better performance
  - Reduces token overhead while maintaining functionality
  - Improves efficiency of agent initialization

## [v0.0.24] - 2025-11-30

### Features

- Add ask/execute mode to control file edits ([#43](https://github.com/presmihaylov/eksecd/pull/43))
  - Introduces ask/execute mode for controlled file editing operations
  - Allows users to review and approve file changes before they are applied
  - Enhances safety and control over agent-initiated file modifications

## [v0.0.23] - 2025-11-29

### Bugfixes

- Return message instead of error on no response ([#42](https://github.com/presmihaylov/eksecd/pull/42))
  - Improves handling of cases where Claude returns no response
  - Returns informative message instead of throwing error
  - Enhances robustness for edge cases in conversation handling

## [v0.0.22] - 2025-11-29

### Features

- Add PR template support to descriptions ([#41](https://github.com/presmihaylov/eksecd/pull/41))
  - Supports custom PR description templates for enhanced pull request workflows
  - Enables teams to standardize PR formatting and content
  - Improves consistency across repository contributions

## [v0.0.21] - 2025-11-16

### Bugfixes

- Fix: Collect all assistant messages in conversation response ([#38](https://github.com/presmihaylov/eksecd/pull/38))
  - Ensures all assistant messages are properly collected in multi-turn conversations
  - Fixes message loss issues in conversation responses
  - Improves reliability of conversation handling

## [v0.0.20] - 2025-11-08

### Features

- Add support for codex ([#37](https://github.com/presmihaylov/eksecd/pull/37))
  - Integrates codex functionality for enhanced code analysis
  - Expands agent capabilities with advanced code understanding
  - Improves code-related task performance

### Improvements

- Add skill-creator from anthropics/skills
  - Includes skill-creator skill for creating custom skills
  - Enables users to extend agent capabilities
  - Provides guided workflow for skill development

## [v0.0.19] - 2025-10-29

### Bugfixes

- Always sync token to environment manager ([#34](https://github.com/presmihaylov/eksecd/pull/34))
  - Ensures OAuth tokens are always synchronized to the environment manager
  - Fixes token sync inconsistencies that could cause authentication failures
  - Improves reliability of token management across agent lifecycle
  - Enhances stability for long-running agent instances

## [v0.0.18] - 2025-10-29

### Features

- Refresh tokens before conversations ([#33](https://github.com/presmihaylov/eksecd/pull/33))
  - Ensures OAuth tokens are refreshed before starting new conversations
  - Prevents mid-conversation authentication failures
  - Improves reliability for long-running agents
  - Enhances user experience with seamless authentication

### Improvements

- Decouple token monitoring from socketio retry ([#32](https://github.com/presmihaylov/eksecd/pull/32))
  - Separates token refresh logic from WebSocket connection management
  - Improves system reliability and error handling
  - Reduces coupling between authentication and communication layers
  - Enhances maintainability of token monitoring logic

## [v0.0.17] - 2025-10-29

### Features

- Add token management with auto-refresh ([#31](https://github.com/presmihaylov/eksecd/pull/31))
  - Implements automatic OAuth token refreshing
  - Improves authentication reliability
  - Reduces manual token management overhead
  - Enhances long-running agent stability

## [v0.0.16] - 2025-10-28

### Features

- Add thread context support for conversations ([#30](https://github.com/presmihaylov/eksecd/pull/30))
  - Implements thread context tracking for multi-turn conversations
  - Improves conversation continuity and context management
  - Enhances agent's ability to maintain conversation state
  - Enables better handling of complex, multi-message interactions

## [v0.0.15] - 2025-10-23

### Features

- Add EKSEC_CONFIG_DIR environment variable ([#28](https://github.com/presmihaylov/eksecd/pull/28))
  - Allows custom configuration directory path
  - Improves deployment flexibility
  - Enables better multi-instance management

### Bugfixes

- Fix parsing of claude responses with large images
  - Resolves issues with handling large image attachments
  - Improves response parsing stability
  - Enhances reliability for image-heavy workflows
- Reduce Socket.IO reconnect max backoff to 10s ([#29](https://github.com/presmihaylov/eksecd/pull/29))
  - Faster reconnection during network issues
  - Reduces downtime during connectivity problems
  - Improves overall agent responsiveness

## [0.0.14] - 2025-10-16

### Features

- Add attachment support with magic bytes ([#26](https://github.com/presmihaylov/eksecd/pull/26))
  - Implements automatic file type detection using magic bytes
  - Supports attachments in agent communication
  - Enhances file handling capabilities

### Bugfix

- Prevent job recovery on socket reconnect ([#27](https://github.com/presmihaylov/eksecd/pull/27))
  - Fixes duplicate job recovery attempts during reconnection
  - Ensures clean reconnection without state conflicts
  - Improves stability during network interruptions

## [0.0.13] - 2025-10-14

### Improvements

- Extend job inactivity timeout to 24 hours ([#23](https://github.com/presmihaylov/eksecd/pull/23))
  - Jobs now remain active for 24 hours instead of 1 hour
  - Prevents premature job termination for long-running tasks
  - Improves reliability for extended coding sessions
- Prevent reconnect blocking by persisting worker pools ([#24](https://github.com/presmihaylov/eksecd/pull/24))
  - Worker pools now persist across socket reconnections
  - Eliminates blocking during reconnection events
  - Ensures continuous operation without interruption

## [0.0.12] - 2025-10-12

### Features

- Add message queue for reliable reconnection ([#22](https://github.com/presmihaylov/eksecd/pull/22))
  - Implements message queue to prevent message loss during reconnection
  - Ensures reliable message delivery with automatic retry mechanism
  - Dramatically improves agent stability and reliability during network interruptions

### Documentation

- Add comprehensive release process documentation
  - Detailed release guide in docs/release_eksecd.md
  - Step-by-step instructions for version bumping and changelog updates
  - Release notes template with emoji formatting examples
  - Troubleshooting section and complete workflow documentation

## [0.0.11] - 2025-10-12

### Features

- Add persistent state with job restoration ([#20](https://github.com/presmihaylov/eksecd/pull/20))
  - Implements state persistence across agent restarts
  - Automatic job restoration on startup
  - Enhanced recovery handling for interrupted tasks
- Add startup logging for config and environment ([#19](https://github.com/presmihaylov/eksecd/pull/19))
  - Improved visibility into agent configuration at startup
  - Environment variable logging for debugging
- Support custom release notes in build script
  - Build script now accepts custom release notes from `/tmp/release_notes.md`

### Documentation

- Add Claude Control context to prompts ([#18](https://github.com/presmihaylov/eksecd/pull/18))
  - Enhanced prompt templates with Claude Control-specific context

## [0.0.3] - 2025-08-22

### Bugfix

- Set the env variables in program env when reloading

## [0.0.2] - 2025-08-17

### Documentation

- Add project overview and architecture guide ([#1](https://github.com/your-org/eksecd/issues/1))

### Refactor

- Improve session context and clean Git methods ([#2](https://github.com/your-org/eksecd/issues/2))

## [0.0.1] - 2025-08-12

### Features

- Testing
- Generate PR titles with git-cliff conventions out of the box
- Add homebrew installation
- Initial eksecd release

### Miscellaneous Tasks

- Fix release script
- Update readme
- Update readme

<!-- generated by git-cliff -->