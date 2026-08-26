# Infer PROJECT_NAME and PROJECT_ORG from git remote or directory path (NEVER hardcode)
PROJECT_NAME := $(shell git remote get-url origin 2>/dev/null | sed -E 's|.*/([^/]+)(\.git)?$$|\1|' || basename "$$(pwd)")
PROJECT_ORG := $(shell git remote get-url origin 2>/dev/null | sed -E 's|.*/([^/]+)/[^/]+(\.git)?$$|\1|' || basename "$$(dirname "$$(pwd)")")

# Version precedence: VERSION env (?= respects it) > release.txt > "devel" fallback
VERSION ?= $(shell cat release.txt 2>/dev/null || echo "devel")

# BuildEpoch — Unix build timestamp (seconds, UTC); the single captured time
# source. Lets the updater detect a newer daily build; BUILD_DATE below is
# derived from it, not captured independently.
BUILD_EPOCH := $(shell date -u +%s)
# Build info — ISO 8601 / RFC 3339 UTC per version_conventions.md, derived from BUILD_EPOCH
# Format: "2025-12-04T13:05:13Z"
BUILD_DATE := $(shell date -u -d @$(BUILD_EPOCH) +"%Y-%m-%dT%H:%M:%SZ")
COMMIT_ID := $(shell git rev-parse --short=7 HEAD 2>/dev/null || echo "N/A")
# COMMIT_ID used directly - no VCS_REF alias

# Official site URL (OPTIONAL - never guess or assume)
# Sources (in order of precedence):
#   1. File: site.txt in project root (single line, URL only)
#   2. Environment variable: OFFICIAL_SITE=https://example.com
#   3. Empty (self-hosted projects - users must use --server flag)
# NEVER infer from project name, domain, or any other source
OFFICIAL_SITE := $(shell [ -f site.txt ] && cat site.txt || echo "$${OFFICIAL_SITE:-}")

# Linker flags to embed build info
# BuildDate is NOT embedded here - it is derived from BuildEpoch in the app's init()
LDFLAGS := -s -w \
	-X 'main.Version=$(VERSION)' \
	-X 'main.CommitID=$(COMMIT_ID)' \
	-X 'main.BuildEpoch=$(BUILD_EPOCH)' \
	-X 'main.OfficialSite=$(OFFICIAL_SITE)'

# Directories
BINDIR := binaries
RELDIR := releases

# Go directories (persistent across builds)
# GO_CACHE: module download cache — safe to share across concurrent builds (Go file-locks writes)
# GO_BUILD: compile cache — scoped per project to prevent corruption when multiple projects build concurrently
GO_CACHE  ?= $(HOME)/go/pkg/mod
GO_BUILD  ?= $(HOME)/.cache/go-build/$(PROJECT_NAME)

# Build targets (space-separated — consumed by shell for-loops; all 8 platforms)
PLATFORMS ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64 freebsd/amd64 freebsd/arm64

# Resource limits — overrideable; prevents any single container from starving concurrent agent builds
DOCKER_MEM  ?= 4g
DOCKER_CPUS ?= 2

# Docker - Set REGISTRY based on your platform (ghcr.io, registry.gitlab.com, git.example.com)
REGISTRY ?= ghcr.io/$(PROJECT_ORG)/$(PROJECT_NAME)
GO_DOCKER := docker run --rm \
	--name $(PROJECT_NAME)-$$(tr -dc 'a-z0-9' </dev/urandom | head -c8) \
	--memory=$(DOCKER_MEM) --cpus=$(DOCKER_CPUS) \
	-v $(PWD):/app \
	-v $(GO_CACHE):/usr/local/share/go/pkg/mod \
	-v $(GO_BUILD):/usr/local/share/go/cache \
	-w /app \
	-e CGO_ENABLED=0 \
	-e GOFLAGS=-buildvcs=false \
	casjaysdev/go:latest
# CGO_ENABLED=0 and GOFLAGS=-buildvcs=false are casjaysdev/go:latest defaults; set explicitly for clarity

.PHONY: build local release docker test dev clean

# =============================================================================
# BUILD - Build all platforms + local binary (via Docker with cached modules)
# =============================================================================
build: clean
	@mkdir -p $(BINDIR) $(GO_CACHE) $(GO_BUILD)
	@echo "Building version $(VERSION)..."

	# Tidy and download modules
	@echo "Tidying and downloading Go modules..."
	@$(GO_DOCKER) go mod tidy
	@$(GO_DOCKER) go mod download

	# Build for local OS/ARCH
	@echo "Building local binary..."
	@$(GO_DOCKER) sh -c "GOOS=$$(go env GOOS) GOARCH=$$(go env GOARCH) \
		go build -buildvcs=false -trimpath -ldflags \"$(LDFLAGS)\" -o $(BINDIR)/$(PROJECT_NAME) ./src"

	# Build server for all platforms
	@for platform in $(PLATFORMS); do \
		OS=$${platform%/*}; \
		ARCH=$${platform#*/}; \
		OUTPUT=$(BINDIR)/$(PROJECT_NAME)-$$OS-$$ARCH; \
		[ "$$OS" = "windows" ] && OUTPUT=$$OUTPUT.exe; \
		echo "Building server $$OS/$$ARCH..."; \
		$(GO_DOCKER) sh -c "GOOS=$$OS GOARCH=$$ARCH \
			go build -buildvcs=false -trimpath -ldflags \"$(LDFLAGS)\" \
			-o $$OUTPUT ./src" || exit 1; \
	done

	# Build CLI for all platforms (if exists)
	@if [ -d "src/client" ]; then \
		for platform in $(PLATFORMS); do \
			OS=$${platform%/*}; \
			ARCH=$${platform#*/}; \
			OUTPUT=$(BINDIR)/$(PROJECT_NAME)-cli-$$OS-$$ARCH; \
			[ "$$OS" = "windows" ] && OUTPUT=$$OUTPUT.exe; \
			echo "Building CLI $$OS/$$ARCH..."; \
			$(GO_DOCKER) sh -c "GOOS=$$OS GOARCH=$$ARCH \
				go build -buildvcs=false -trimpath -ldflags \"$(LDFLAGS)\" \
				-o $$OUTPUT ./src/client" || exit 1; \
		done; \
	fi

	# Build agent for all platforms (if exists)
	@if [ -d "src/agent" ]; then \
		for platform in $(PLATFORMS); do \
			OS=$${platform%/*}; \
			ARCH=$${platform#*/}; \
			OUTPUT=$(BINDIR)/$(PROJECT_NAME)-agent-$$OS-$$ARCH; \
			[ "$$OS" = "windows" ] && OUTPUT=$$OUTPUT.exe; \
			echo "Building agent $$OS/$$ARCH..."; \
			$(GO_DOCKER) sh -c "GOOS=$$OS GOARCH=$$ARCH \
				go build -buildvcs=false -trimpath -ldflags \"$(LDFLAGS)\" \
				-o $$OUTPUT ./src/agent" || exit 1; \
		done; \
	fi

	@echo "Build complete: $(BINDIR)/"

# =============================================================================
# LOCAL - Build local binaries only (fast development builds)
# =============================================================================
local: clean
	@mkdir -p $(BINDIR) $(GO_CACHE) $(GO_BUILD)
	@echo "Building local binaries version $(VERSION)..."

	# Tidy and download modules
	@echo "Tidying and downloading Go modules..."
	@$(GO_DOCKER) go mod tidy
	@$(GO_DOCKER) go mod download

	# Build server binary
	@echo "Building $(PROJECT_NAME)..."
	@$(GO_DOCKER) sh -c "GOOS=$$(go env GOOS) GOARCH=$$(go env GOARCH) \
		go build -buildvcs=false -trimpath -ldflags \"$(LDFLAGS)\" -o $(BINDIR)/$(PROJECT_NAME) ./src"

	# Build CLI binary (if exists)
	@if [ -d "src/client" ]; then \
		echo "Building $(PROJECT_NAME)-cli..."; \
		$(GO_DOCKER) sh -c "GOOS=$$(go env GOOS) GOARCH=$$(go env GOARCH) \
			go build -buildvcs=false -trimpath -ldflags \"$(LDFLAGS)\" -o $(BINDIR)/$(PROJECT_NAME)-cli ./src/client"; \
	fi

	# Build agent binary (if exists)
	@if [ -d "src/agent" ]; then \
		echo "Building $(PROJECT_NAME)-agent..."; \
		$(GO_DOCKER) sh -c "GOOS=$$(go env GOOS) GOARCH=$$(go env GOARCH) \
			go build -buildvcs=false -trimpath -ldflags \"$(LDFLAGS)\" -o $(BINDIR)/$(PROJECT_NAME)-agent ./src/agent"; \
	fi

	@echo "Local build complete: $(BINDIR)/"

# =============================================================================
# RELEASE - Manual local release (stable only)
# =============================================================================
release: build
	@mkdir -p $(RELDIR)
	@echo "Preparing release $(VERSION)..."

	# Create version.txt
	@echo "$(VERSION)" > $(RELDIR)/version.txt

	# Copy binaries to releases (strip if needed)
	@for f in $(BINDIR)/$(PROJECT_NAME)-*; do \
		[ -f "$$f" ] || continue; \
		strip "$$f" 2>/dev/null || true; \
		cp "$$f" $(RELDIR)/; \
	done

	# Create source archive (exclude VCS and build artifacts)
	@tar --exclude='.git' --exclude='.github' --exclude='.gitea' \
		--exclude='binaries' --exclude='releases' --exclude='*.tar.gz' \
		-czf $(RELDIR)/$(PROJECT_NAME)-$(VERSION)-source.tar.gz .

	# Delete existing release/tag if exists
	@gh release delete $(VERSION) --yes 2>/dev/null || true
	@git tag -d $(VERSION) 2>/dev/null || true
	@git push origin :refs/tags/$(VERSION) 2>/dev/null || true

	# Create new release (stable)
	@gh release create $(VERSION) $(RELDIR)/* \
		--title "$(PROJECT_NAME) $(VERSION)" \
		--notes "Release $(VERSION)" \
		--latest

	@echo "Release complete: $(VERSION)"

# =============================================================================
# DOCKER - Build and push container to registry (set REGISTRY env var)
# =============================================================================
# Uses multi-stage Dockerfile - Go compilation happens inside Docker
# No pre-built binaries needed
docker:
	@echo "Building Docker image $(VERSION)..."

	# Ensure buildx is available
	@docker buildx version > /dev/null 2>&1 || (echo "docker buildx required" && exit 1)

	# Create/use builder
	@docker buildx create --name $(PROJECT_NAME)-builder --use 2>/dev/null || \
		docker buildx use $(PROJECT_NAME)-builder

	# Build multi-arch locally (no push — pushing is CI/CD's responsibility)
	@docker buildx build \
		-f docker/Dockerfile \
		--platform linux/amd64,linux/arm64 \
		--build-arg VERSION="$(VERSION)" \
		--build-arg BUILD_DATE="$(BUILD_DATE)" \
		--build-arg BUILD_EPOCH="$(BUILD_EPOCH)" \
		--build-arg COMMIT_ID="$(COMMIT_ID)" \
		--build-arg OFFICIAL_SITE="$(OFFICIAL_SITE)" \
		-t $(REGISTRY):$(VERSION) \
		-t $(REGISTRY):latest \
		.

	@echo "Docker build complete: $(REGISTRY):$(VERSION)"

# =============================================================================
# TEST - Run all tests with coverage enforcement (via Docker)
# =============================================================================
test:
	@echo "Running tests with coverage..."
	@$(GO_DOCKER) sh -c " \
		mkdir -p \"\$${TMPDIR:-/tmp}/$(PROJECT_ORG)\" && \
		COVDIR=\$$(mktemp -d \"\$${TMPDIR:-/tmp}/$(PROJECT_ORG)/$(PROJECT_NAME)-XXXXXX\") && \
		go mod download && \
		go test -v -cover -coverprofile=\$$COVDIR/coverage.out ./... && \
		COVERAGE=\$$(go tool cover -func=\$$COVDIR/coverage.out | grep total | awk '{print \$$3}' | sed 's/%//') && \
		echo \"Coverage: \$$COVERAGE%\" && \
		if [ \$$(echo \"\$$COVERAGE < 60\" | bc -l) -eq 1 ]; then \
			echo \"ERROR: Coverage is \$$COVERAGE%, must be >= 60%\"; exit 1; \
		fi && \
		echo \"Tests complete - Coverage: \$$COVERAGE% (>= 60% required) ✓\""

# =============================================================================
# DEV - Quick build for local development/testing (to random temp dir)
# =============================================================================
# Fast: local platform only, no ldflags, random temp dir for isolation
# Builds server + CLI + agent (if they exist)
dev:
	@$(GO_DOCKER) go mod tidy
	@mkdir -p "$${TMPDIR:-/tmp}/$(PROJECT_ORG)" && \
		BUILD_DIR=$$(mktemp -d "$${TMPDIR:-/tmp}/$(PROJECT_ORG)/$(PROJECT_NAME)-XXXXXX") && \
		echo "Quick dev build to $$BUILD_DIR..." && \
		$(GO_DOCKER) go build -buildvcs=false -o $$BUILD_DIR/$(PROJECT_NAME) ./src && \
		echo "Built: $$BUILD_DIR/$(PROJECT_NAME)" && \
		if [ -d "src/client" ]; then \
			$(GO_DOCKER) go build -buildvcs=false -o $$BUILD_DIR/$(PROJECT_NAME)-cli ./src/client && \
			echo "Built: $$BUILD_DIR/$(PROJECT_NAME)-cli"; \
		fi && \
		if [ -d "src/agent" ]; then \
			$(GO_DOCKER) go build -buildvcs=false -o $$BUILD_DIR/$(PROJECT_NAME)-agent ./src/agent && \
			echo "Built: $$BUILD_DIR/$(PROJECT_NAME)-agent"; \
		fi && \
		echo "Test:  docker run --rm --name $(PROJECT_NAME)-test -v $$BUILD_DIR:/app alpine:latest /app/$(PROJECT_NAME) --help"

# =============================================================================
# CLEAN - Remove build artifacts
# =============================================================================
clean:
	@rm -rf $(BINDIR) $(RELDIR)
