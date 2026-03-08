# nairid

The Nairi daemon - runs your AI coding agents and connects them to the [Nairi platform](https://nairi.ai). nairid bridges AI assistants (Claude Code, Cursor, Codex, OpenCode) with team collaboration platforms like Slack and Discord.

### Supported AI Assistants

- **Claude Code**: Anthropic's official CLI tool for software engineering (default)
- **Cursor**: Popular AI-powered code editor integration
- **Codex**: OpenAI's coding assistant with model selection
- **OpenCode**: Open-source AI coding agent with multi-provider model support

### Supported Platforms

nairid runs on **macOS**, **Linux**, and **Windows** with native binaries for both Intel and ARM architectures.

## Installation

### Via Homebrew (Recommended)

```bash
brew install presmihaylov/taps/nairid
```

To upgrade to the latest version:
```bash
brew upgrade presmihaylov/taps/nairid
```

### From Source
You will need to have Go 1.24 installed:
```bash
git clone https://github.com/nairiai/nairid.git
cd nairid
make build
```

The compiled binary will be available at `bin/nairid`.

## Usage

### Prerequisites

- Git
- GitHub CLI (`gh`) - [Install here](https://cli.github.com/)
- Nairi account (sign up [here](https://nairi.ai))

### Basic Usage

#### Repository Setup

**Important**: nairid will autonomously create branches, make changes, and create pull requests. To avoid conflicts with your main development workflow, it's **strongly recommended** to clone your repository separately for nairid use.

#### Prerequisites Setup

Before running nairid, ensure you have:

1. **GitHub CLI Authentication**: nairid uses the GitHub CLI to create pull requests
   ```bash
   # Login to GitHub
   gh auth login
   
   # Verify authentication
   gh auth status
   ```

2. **Repository Access**: If using SSH, ensure your key is loaded
   ```bash
   ssh-add ~/.ssh/id_rsa
   ```

#### GitHub Account Options

You can use nairid with:
- **Your personal GitHub account**: nairid will create PRs on your behalf
- **Dedicated bot account**: Create a separate GitHub account for nairid to use (recommended for teams)

#### Environment Setup

nairid requires the following environment variables:

```bash
# Required: API key from your Nairi organization
export NAIRI_API_KEY=your_api_key_here

# Alternative (backwards compatible):
# export EKSEC_API_KEY=your_api_key_here
```

You can generate an API key from the Nairi dashboard.

#### Running nairid

Once setup is complete, run nairid in your repository directory.

By default, nairid uses Claude Code as the AI assistant with `acceptEdits` permission mode.

### Command Line Options

```bash
nairid [OPTIONS]

Options:
  --agent=[claude|cursor|codex|opencode]  AI assistant to use (default: claude)
  --claude-bypass-permissions             Use bypassPermissions for Claude/Codex (sandbox only)
  --model=MODEL                           Model to use (agent-specific, see examples below)
  -v, --version                           Show version information
  -h, --help                              Show help message
```

### Agent-Specific Usage

#### Claude Code Agent (Default)
```bash
# Standard mode - requires approval for file edits
nairid --agent claude

# Use specific model (options: sonnet, haiku, opus, or full model names like claude-sonnet-4-5-20250929)
nairid --agent claude --model haiku

# Bypass permissions (Recommended in a secure sandbox environment only)
nairid --agent claude --claude-bypass-permissions
```

#### Cursor Agent
```bash
# Use Cursor with specific model (options: gpt-5, sonnet-4, sonnet-4-thinking)
nairid --agent cursor --model sonnet-4
```

#### Codex Agent
```bash
# Standard mode - requires approval for file edits
nairid --agent codex

# Bypass permissions (Recommended in a secure sandbox environment only)
nairid --agent codex --claude-bypass-permissions

# Use specific model (default: gpt-5, accepts any model string)
nairid --agent codex --model gpt-5
```

#### OpenCode Agent
```bash
# OpenCode requires bypass permissions mode (default model: opencode/grok-code)
nairid --agent opencode --claude-bypass-permissions

# Use specific provider/model (format: provider/model)
nairid --agent opencode --claude-bypass-permissions --model anthropic/claude-3-5-sonnet
```

**Note**: OpenCode only supports `bypassPermissions` mode. The `--claude-bypass-permissions` flag is required.

### Logging
nairid automatically creates log files in `~/.config/eksecd/logs/` with timestamp-based naming. Logs are written to both stdout and files for debugging.

## Development

### Building

```bash
# Build for current platform
make build

# Clean build artifacts
make clean
```

### Testing

```bash
# Run all tests
make test

# Run tests with verbose output
make test-verbose
```

### Linting

```bash
# Run linter
make lint

# Auto-fix linting issues
make lint-fix
```

## Security Recommendations

nairid operates in different permission modes depending on the AI assistant and configuration:

### Secure Mode (Recommended)
- **Claude Code (default)**: Runs in `acceptEdits` mode, requiring explicit approval for all file modifications
- **Codex (default)**: Runs in `acceptEdits` mode with sandbox protections
- **Best Practice**: Use this mode when running nairid on your local development machine

### Bypass Permissions Mode
- **Claude Code with `--claude-bypass-permissions`**: Allows unrestricted system access
- **Codex with `--claude-bypass-permissions`**: Bypasses approvals and sandbox
- **Cursor Agent**: **Always runs in bypass mode by default**
- **OpenCode Agent**: **Only supports bypass mode**

When running in bypass permissions mode, **anyone with access to your Slack workspace or Discord server can execute arbitrary commands on your system with your user privileges**. It's recommended that you use this mode only if you're running the agent in a secure environment like a docker container or a remote, isolated server.

## Contributing

Fork the repository and open a pull request. Contributions are welcome!

## License

MIT License - see [LICENSE](LICENSE) file for details.

## Support

Contact us at support@nairi.ai
