module := "github.com/steveclarke/ucmix"
bin := "ucmix"

# List available recipes
default:
    @just --list

# Install pinned toolchain (go, gotestsum, golangci-lint, goreleaser, govulncheck, bats)
setup:
    mise install

# Build the CLI to dist/ with version info injected
build:
    go build -ldflags "-X {{module}}/cmd.version=dev-$(git rev-parse --short HEAD) -X {{module}}/cmd.commit=$(git rev-parse --short HEAD) -X {{module}}/cmd.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o dist/{{bin}} .

# Run unit tests (readable output)
test:
    gotestsum --format testdox ./...

# Run unit tests (compact output)
test-short:
    gotestsum --format dots ./...

# Run unit tests with the race detector
test-race:
    gotestsum --format testdox -- -race ./...

# Build the binary and run the BATS end-to-end suite
test-e2e: build
    bats test/

# Run unit + e2e tests
test-all: test test-e2e

# Lint
lint:
    golangci-lint run

# go vet
vet:
    go vet ./...

# Format
fmt:
    gofmt -w .

# Check formatting
fmt-check:
    gofmt -l .

# Tidy modules
tidy:
    go mod tidy

# Fail if go.mod/go.sum are not tidy
tidy-check:
    go mod tidy && git diff --exit-code go.mod go.sum

# Scan for known vulnerabilities
vulncheck:
    govulncheck ./...

# Install to GOBIN
install:
    go install -ldflags "-X {{module}}/cmd.version=dev-$(git rev-parse --short HEAD)" .

# Run without building
run *args:
    go run . {{args}}

# Remove build artifacts
clean:
    rm -rf dist/

# Show version
version:
    @git describe --tags --always
