# eksecd Project Overview

## What is eksecd?

eksecd is a Go-based CLI application that serves as a bridge between AI coding assistants (Claude Code, Cursor, Codex, OpenCode) and team collaboration platforms (Slack, Discord) through the eksecd platform. It enables teams to interact with AI coding assistants directly from their chat platforms while maintaining proper git workflow and branch management.

## Core Architecture

### Main Components

1. **CLI Agent Interface** (`services/services.go`)
   - Abstraction layer for different AI assistants (Claude, Cursor, Codex, OpenCode)
   - Handles conversation management and session tracking

2. **Socket.IO Client** (`cmd/main.go`)
   - Maintains persistent connection to Claude Control platform
   - Routes messages between chat platforms and AI assistants
   - Dual worker pool architecture for sequential conversations and parallel PR checks

3. **Message Handlers** (`handlers/messages.go`)
   - Processes different message types (start conversation, user messages, idle job checks)
   - Manages conversation lifecycle and git operations

4. **Git Integration** (`usecases/git.go`)
   - Automatic branch creation and management
   - Auto-commit functionality
   - Pull request creation and status tracking

5. **Application State** (`models/app_state.go`)
   - In-memory tracking of active jobs and conversations
   - Session management for AI assistant interactions

### Supported AI Assistants

- **Claude Code** (default): Anthropic's CLI tool with configurable permission modes and model selection (use `--model=sonnet|haiku|opus` or full model name)
- **Cursor**: AI-powered code editor integration with model selection
- **Codex**: OpenAI's coding assistant with model selection
- **OpenCode**: Open-source AI coding agent with multi-provider model support (use `--model=provider/model`). **Only supports `bypassPermissions` mode**.

### Key Features

- **Branch Management**: Auto-creates eksecd-prefixed branches for each conversation
- **Permission Modes**:
  - `acceptEdits` (secure, requires approval) - supported by Claude Code and Codex
  - `bypassPermissions` (sandbox only, unrestricted access) - supported by all agents
  - Note: OpenCode **only supports `bypassPermissions`** and will fail if `acceptEdits` is requested
- **Auto-commit**: Automatically commits changes with descriptive messages
- **PR Management**: Creates and tracks pull requests automatically
- **Job Lifecycle**: Tracks conversation sessions and cleans up completed jobs
- **Directory Locking**: Prevents multiple instances in same directory

## Development Commands

- **Build**: `make build` - Creates binary in `bin/eksecd`
- **Test**: `make test` or `make test-verbose`
- **Lint**: `make lint` or `make lint-fix`
- **Clean**: `make clean` - Removes build artifacts
- **Release**: `make release` - Creates production release with binaries (see Release Process below)

## Release Process

**IMPORTANT**: When asked to create a new release for eksecd, you MUST read and follow the detailed instructions in **[docs/release_eksecd.md](docs/release_eksecd.md)**.

The release process includes:
1. Cloning repository to temporary directory
2. Analyzing changes since last release
3. Bumping version (patch/minor/major)
4. Updating CHANGELOG.md with categorized changes
5. Committing and pushing version bump
6. Creating formatted release notes (with emojis, following established format)
7. Building binaries for all platforms
8. Creating GitHub release with all binaries and checksums

**Do not attempt to create a release without consulting the documentation first.**

## Environment Requirements

- `EKSEC_API_KEY`: Required API key from eksecd platform
- `EKSEC_WS_API_URL`: Optional WebSocket URL (defaults to production)
- Git and GitHub CLI (`gh`) must be configured
- Go 1.24+ for building from source

## Security Considerations

- Secure mode (acceptEdits) recommended for local development (Claude Code, Codex)
- Bypass permissions mode should only be used in controlled sandbox environments
- Directory locking prevents concurrent instances
- All git operations are tracked and logged
- OpenCode **only supports `bypassPermissions` mode**

### Customer Data Privacy

**NEVER** include real production data in commits, PRs, test cases, or code examples. This includes customer names, organization names, user counts, query results, metric values, or any data observed from production systems. All commits and PRs are public. Always use obviously fake/placeholder data in tests (e.g. "Found 42 results" not real counts from production).

## Log Management

- Logs stored in `~/.config/eksecd/logs/`
- Automatic cleanup of logs older than 7 days
- Both stdout and file logging for debugging
