#!/bin/bash

set -e

# Get version from VERSION file
VERSION=$(cat core/VERSION)
TAG="$VERSION"
TARGET_DIR="bin"
TEMP_DIR=$(mktemp -d)

function create_build {
    GOOS=$1
    GOARCH=$2
    EXT=$3
    if [ -z "$EXT" ]; then
        EXT=$GOARCH
    fi

    if [ "$GOOS" = "windows" ]; then
        BINARY=nairid-$TAG-$GOOS-$EXT.exe
    else
        BINARY=nairid-$TAG-$GOOS-$EXT
    fi
    
    echo "Building $BINARY..."
    CGO_ENABLED=0 GOOS=$GOOS GOARCH=$GOARCH go build -ldflags="-s -w" -o $TEMP_DIR/$BINARY cmd/*.go
    cd $TEMP_DIR && shasum -a 256 $BINARY > $BINARY.sha256 && cd - > /dev/null
}

echo "Creating production binaries for nairid $TAG..."

# Check if gh CLI is available
if ! command -v gh &> /dev/null; then
    echo "Error: GitHub CLI (gh) is required but not installed."
    echo "Please install it from: https://cli.github.com/"
    exit 1
fi

# Check if user is authenticated with gh
if ! gh auth status &> /dev/null; then
    echo "Error: Not authenticated with GitHub CLI."
    echo "Please run: gh auth login"
    exit 1
fi

# Ensure target directory exists
mkdir -p $TARGET_DIR

# Build for all platforms
create_build windows amd64 x86_64
create_build darwin amd64 x86_64
create_build darwin arm64 
create_build linux amd64 x86_64
create_build linux arm64

# Check if tag already exists
if git rev-parse "$TAG" >/dev/null 2>&1; then
    echo "Warning: Tag $TAG already exists. Deleting and recreating..."
    git tag -d "$TAG" || true
    git push origin ":$TAG" || true
fi

# Create and push version tag
echo "Creating and pushing tag $TAG..."
git tag -a "$TAG" -m "Release $TAG"
git push origin "$TAG"

# Update latest tag to point to this release
echo "Updating 'latest' tag..."
if git rev-parse "latest" >/dev/null 2>&1; then
    echo "Deleting existing 'latest' tag..."
    git tag -d "latest"
    git push origin ":latest" || true
fi

echo "Creating new 'latest' tag..."
git tag -a "latest" -m "Latest release - $TAG"
git push origin "latest"

# Check if release notes file exists, otherwise use default
if [ -f "/tmp/release_notes.md" ]; then
    RELEASE_NOTES=$(cat /tmp/release_notes.md)
else
    RELEASE_NOTES="## Changes
- Version $VERSION release"
fi

# Create GitHub release with all binaries
echo "Creating GitHub release $TAG..."

gh release create "$TAG" \
    --title "nairid $TAG" \
    --notes "$RELEASE_NOTES" \
    $TEMP_DIR/nairid-$TAG-*

# Cleanup
rm -rf $TEMP_DIR

echo "Production release created: $TAG"
echo "Binaries uploaded to: https://github.com/$(gh repo view --json owner,name -q '.owner.login + "/" + .name')/releases/tag/$TAG"
