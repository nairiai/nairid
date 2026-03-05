.PHONY: run build clean release test test-verbose lint lint-fix version-patch version-minor version-major

run:
	go run cmd/*.go

build:
	mkdir -p bin
	go build -o bin/eksecd cmd/*.go

clean:
	rm -rf bin/

release:
	./scripts/build_binaries.sh

test:
	go test -count=1 ./...

test-verbose:
	go test -count=1 -v ./...

lint:
	golangci-lint run

lint-fix:
	golangci-lint run --fix


version-patch:
	@CURRENT_VERSION=$$(cat core/VERSION); \
	MAJOR=$$(echo $$CURRENT_VERSION | cut -d'.' -f1); \
	MINOR=$$(echo $$CURRENT_VERSION | cut -d'.' -f2); \
	PATCH=$$(echo $$CURRENT_VERSION | cut -d'.' -f3); \
	NEW_PATCH=$$(($$PATCH + 1)); \
	NEW_VERSION="$$MAJOR.$$MINOR.$$NEW_PATCH"; \
	echo "Bumping version from $$CURRENT_VERSION to $$NEW_VERSION"; \
	echo "$$NEW_VERSION" > core/VERSION

version-minor:
	@CURRENT_VERSION=$$(cat core/VERSION); \
	MAJOR=$$(echo $$CURRENT_VERSION | cut -d'.' -f1); \
	MINOR=$$(echo $$CURRENT_VERSION | cut -d'.' -f2); \
	NEW_MINOR=$$(($$MINOR + 1)); \
	NEW_VERSION="$$MAJOR.$$NEW_MINOR.0"; \
	echo "Bumping version from $$CURRENT_VERSION to $$NEW_VERSION"; \
	echo "$$NEW_VERSION" > core/VERSION

version-major:
	@CURRENT_VERSION=$$(cat core/VERSION); \
	MAJOR=$$(echo $$CURRENT_VERSION | cut -d'.' -f1); \
	NEW_MAJOR=$$(($$MAJOR + 1)); \
	NEW_VERSION="$$NEW_MAJOR.0.0"; \
	echo "Bumping version from $$CURRENT_VERSION to $$NEW_VERSION"; \
	echo "$$NEW_VERSION" > core/VERSION
