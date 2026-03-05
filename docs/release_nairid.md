# EkSecD Release Process

This document describes the complete process for creating a new release of nairid, including version bumping, changelog updates, binary builds, and GitHub release creation.

## Prerequisites

Before starting, ensure you have:

- **Go compiler** (version 1.23+) installed at `/usr/local/go/bin`
- **GitHub CLI** (`gh`) installed and authenticated
- **SSH access** to the presmihaylov/nairid repository
- **Write permissions** to the repository

### Installing Go (if needed)

```bash
# Download and install Go
wget https://go.dev/dl/go1.23.2.linux-amd64.tar.gz -O /tmp/go1.23.2.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf /tmp/go1.23.2.linux-amd64.tar.gz

# Verify installation
export PATH=$PATH:/usr/local/go/bin
go version
```

### Verify GitHub CLI Authentication

```bash
gh auth status
# Should show you're logged in to github.com
```

## Release Process Overview

1. Clone repository to temporary directory
2. Analyze changes since last release
3. Bump version using Makefile
4. Update CHANGELOG.md with detailed changes
5. Commit and push version bump
6. Create custom release notes
7. Build binaries and create GitHub release

## Step-by-Step Instructions

### 1. Clone Repository

```bash
# Create temp directory and clone
TEMP_DIR=$(mktemp -d)
git clone https://github.com/presmihaylov/nairid.git $TEMP_DIR/nairid
cd $TEMP_DIR/nairid

# Change remote to SSH for push access
git remote set-url origin git@github.com:presmihaylov/nairid.git
```

### 2. Analyze Changes Since Last Release

```bash
# Find the latest release tag
git tag -l | sort -V | tail -5

# View commits since last release (replace v0.0.X with actual version)
git log v0.0.X..HEAD --oneline

# Get detailed commit messages
git log v0.0.X..HEAD --pretty=format:"%s%n%b" --reverse

# For each significant commit, examine the changes
git show <commit-hash> --stat
```

**What to look for:**
- Feature additions (feat: prefix)
- Bug fixes (fix: prefix)
- Documentation changes (docs: prefix)
- Refactoring (refactor: prefix)
- Breaking changes
- Pull request numbers in commit messages

### 3. Determine Version Bump Type

Based on the changes, decide which version component to bump:

- **Patch (0.0.X → 0.0.X+1)**: Bug fixes, minor improvements, documentation
- **Minor (0.X.0 → 0.X+1.0)**: New features, non-breaking changes
- **Major (X.0.0 → X+1.0.0)**: Breaking changes, major rewrites

### 4. Bump Version

```bash
# For patch version bump (most common)
make version-patch

# For minor version bump
make version-minor

# For major version bump
make version-major

# Verify the new version
cat core/VERSION
```

This updates the `core/VERSION` file.

### 5. Update CHANGELOG.md

The changelog should follow the existing format and include:

- Release version and date (YYYY-MM-DD format)
- Categorized changes (Features, Bug Fixes, Documentation, etc.)
- Pull request/issue references where applicable
- Brief descriptions of each change

**Example format:**

```markdown
## [0.0.11] - 2025-10-12

### Features

- Add persistent state with job restoration ([#20](https://github.com/presmihaylov/nairid/pull/20))
  - Implements state persistence across agent restarts
  - Automatic job restoration on startup
  - Enhanced recovery handling for interrupted tasks
- Add startup logging for config and environment ([#19](https://github.com/presmihaylov/nairid/pull/19))
  - Improved visibility into agent configuration at startup
  - Environment variable logging for debugging

### Documentation

- Add Claude Control context to prompts ([#18](https://github.com/presmihaylov/nairid/pull/18))
  - Enhanced prompt templates with Claude Control-specific context
```

Edit the CHANGELOG.md file to add the new version section at the top, after the header.

### 6. Commit and Push Changes

```bash
# Configure git if needed
git config user.name "nairid"
git config user.email "agent@nairi.ai"

# Stage and commit the version bump
git add core/VERSION CHANGELOG.md
git commit -m "chore: bump version to vX.X.X and update changelog"

# Push to main branch
git push origin main
```

### 7. Create Custom Release Notes

Create a file `/tmp/release_notes.md` with formatted release notes. The format should match previous releases with:

- Emojis for visual appeal
- Bold key phrases
- Clear sections
- Enthusiastic tone

**Template:**

```markdown
## 🚀 What's New in vX.X.X

This release **[main theme/improvement]** by [brief description].

### ✨ Key Improvements

**[Emoji] [Feature Name]**
- **[Bold key point]**: [Description]
- **[Bold key point]**: [Description]
- **[Bold key point]**: [Description]
- **[Bold key point]**: [Description]

**[Emoji] [Another Feature]**
- **[Bold key point]**: [Description]
- **[Bold key point]**: [Description]

### 📦 Changes Since vX.X.X
- **feat**: [description] (#PR)
- **fix**: [description] (#PR)
- **docs**: [description] (#PR)

---
These improvements [summary of benefits]! 🎉
```

**Reference format from v0.0.10:**
```markdown
## 🚀 What's New in v0.0.11

This release **enhances reliability and observability** by implementing persistent state management, ensuring nairid can seamlessly recover from restarts and providing better visibility into configuration and operations.

### ✨ Key Improvements

**💾 Persistent State with Job Restoration**
- **Survives restarts**: Agent state persists across restarts, preventing work loss
- **Automatic recovery**: Jobs are automatically restored on startup, picking up right where they left off
- **Enhanced resilience**: Improved recovery handling for interrupted tasks ensures continuity
- **Production-ready**: Agents can now handle unexpected restarts without losing context

**📊 Startup Logging & Observability**
- **Configuration visibility**: Logs full agent configuration at startup for easier debugging
- **Environment transparency**: Displays environment variables to quickly identify misconfiguration
- **Better troubleshooting**: Makes deployment issues much easier to diagnose and resolve

### 📦 Changes Since v0.0.10
- **feat**: add persistent state with job restoration (#20)
- **feat**: add startup logging for config and env (#19)
- **docs**: add Claude Control context to prompts (#18)
- **feat**: support custom release notes in build script

---
These improvements make nairid more reliable in production environments, with better recovery mechanisms and visibility into agent operations! 🎉
```

Create this file before running the release build.

### 8. Build Release and Create GitHub Release

```bash
# Ensure Go is in PATH
export PATH=$PATH:/usr/local/go/bin

# Run the release script
# This will:
# - Build binaries for all platforms (Windows, macOS x86/ARM, Linux x86/ARM)
# - Generate SHA256 checksums for each binary
# - Create git tag for the version
# - Update 'latest' tag
# - Create GitHub release with binaries attached
# - Use release notes from /tmp/release_notes.md
make release
```

The script will:
1. Build 5 platform-specific binaries
2. Generate SHA256 checksums
3. Create and push git tags (`vX.X.X` and `latest`)
4. Create GitHub release
5. Upload all binaries and checksums

### 9. Verify Release

```bash
# View the release
gh release view vX.X.X

# Check the release URL
echo "Release created at: https://github.com/presmihaylov/nairid/releases/tag/vX.X.X"
```

Verify:
- All 5 binaries are present (Windows, macOS x86/ARM, Linux x86/ARM)
- SHA256 checksums are included
- Release notes are properly formatted with emojis
- Tag `latest` points to the new version

## Common Emojis for Release Notes

Use these emojis to maintain consistency:

- 🚀 - What's New header
- ✨ - Key Improvements section
- 💾 - State/persistence features
- 📊 - Logging/observability features
- 🔗 - Connection/networking features
- 🌿 - Git-related features
- 🐛 - Bug fixes
- 📝 - Documentation/release notes
- 📚 - Documentation features
- 📦 - Changes/package section
- ♾️ - Unlimited/continuous features
- 🎉 - Closing celebration

## Release Checklist

- [ ] Repository cloned to temporary directory
- [ ] Changes since last release analyzed
- [ ] Version bumped appropriately (patch/minor/major)
- [ ] CHANGELOG.md updated with categorized changes
- [ ] Changes committed and pushed to main
- [ ] `/tmp/release_notes.md` created with formatted notes
- [ ] Release notes follow established format with emojis
- [ ] Go compiler available in PATH
- [ ] `make release` executed successfully
- [ ] GitHub release created with all binaries
- [ ] Release notes properly formatted on GitHub
- [ ] Tags `vX.X.X` and `latest` pushed successfully

## Troubleshooting

### Go Not Installed

```bash
# Install Go as described in Prerequisites section
wget https://go.dev/dl/go1.23.2.linux-amd64.tar.gz -O /tmp/go1.23.2.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf /tmp/go1.23.2.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin
```

### GitHub CLI Not Authenticated

```bash
gh auth login
# Follow prompts to authenticate
```

### Git Push Fails with HTTPS

```bash
# Change remote to SSH
git remote set-url origin git@github.com:presmihaylov/nairid.git
```

### Tag Already Exists

The build script will automatically delete and recreate tags if they exist. If you need to manually handle:

```bash
# Delete local tag
git tag -d vX.X.X

# Delete remote tag
git push origin :vX.X.X

# Recreate tag
git tag -a vX.X.X -m "Release vX.X.X"
git push origin vX.X.X
```

### Release Notes Not Formatted Properly

Edit the release after creation:

```bash
# Edit existing release
gh release edit vX.X.X --notes-file /tmp/release_notes_updated.md
```

## Build Script Details

The `scripts/build_binaries.sh` script:

1. Reads version from `core/VERSION`
2. Builds binaries for:
   - `windows/amd64` (x86_64)
   - `darwin/amd64` (x86_64)
   - `darwin/arm64`
   - `linux/amd64` (x86_64)
   - `linux/arm64`
3. Generates SHA256 checksums for each binary
4. Creates git tags (`vX.X.X` and `latest`)
5. Reads custom release notes from `/tmp/release_notes.md`
6. Creates GitHub release using `gh` CLI
7. Uploads all binaries and checksums

## Post-Release

After successful release:

1. Verify release is live at `https://github.com/presmihaylov/nairid/releases/tag/vX.X.X`
2. Test download and installation of at least one binary
3. Update any deployment configurations to use new version
4. Announce release in relevant channels (if applicable)
5. Clean up temporary directory: `rm -rf $TEMP_DIR`

## Example Complete Workflow

```bash
# 1. Setup
TEMP_DIR=$(mktemp -d)
git clone https://github.com/presmihaylov/nairid.git $TEMP_DIR/nairid
cd $TEMP_DIR/nairid
git remote set-url origin git@github.com:presmihaylov/nairid.git

# 2. Analyze changes
git tag -l | sort -V | tail -1  # Get last version
LAST_VERSION=$(git tag -l | sort -V | tail -1)
git log $LAST_VERSION..HEAD --oneline

# 3. Bump version
make version-patch

# 4. Update CHANGELOG.md (manual edit)
# Edit CHANGELOG.md to add new version section

# 5. Commit and push
git config user.name "nairid"
git config user.email "agent@nairi.ai"
git add core/VERSION CHANGELOG.md
git commit -m "chore: bump version to $(cat core/VERSION) and update changelog"
git push origin main

# 6. Create release notes
cat > /tmp/release_notes.md << 'EOF'
## 🚀 What's New in vX.X.X
[... release notes content ...]
EOF

# 7. Build and release
export PATH=$PATH:/usr/local/go/bin
make release

# 8. Verify
gh release view $(cat core/VERSION)

# 9. Cleanup
cd ~
rm -rf $TEMP_DIR
```

---

**Last Updated**: 2025-10-12
**Maintainer**: nairid DevOps Agent
