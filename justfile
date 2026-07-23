module := "github.com/steveclarke/ucmix"
bin := "ucmix"
main := "./cmd/ucmix"
ldflags := "-X " + module + "/internal/cli.version=dev-$(git rev-parse --short HEAD) -X " + module + "/internal/cli.commit=$(git rev-parse --short HEAD) -X " + module + "/internal/cli.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)"

# List available recipes
default:
    @just --list

# Install the pinned toolchain (go, gotestsum, golangci-lint, goreleaser, govulncheck, bats)
setup:
    mise install

# Build the CLI to dist/ with version info injected
build:
    @mkdir -p dist
    go build -ldflags "{{ldflags}}" -o dist/{{bin}} {{main}}

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
    go build -o dist/fakeboard ./cmd/fakeboard
    bats test/

# Run unit + e2e tests
test-all: test test-e2e

# Run hardware tests against a real mixer (needs UCMIX_MIXER_ADDR; never in CI)
test-hw:
    go test -tags hardware ./...

# Lint
lint:
    golangci-lint run

# go vet
vet:
    go vet ./...

# Format
fmt:
    gofmt -w .

# Check formatting (fails if anything is unformatted)
fmt-check:
    @test -z "$(gofmt -l .)" || (gofmt -l . && exit 1)

# Tidy modules
tidy:
    go mod tidy

# Fail if go.mod/go.sum are not tidy
tidy-check:
    go mod tidy && git diff --exit-code go.mod go.sum

# Scan for known vulnerabilities
vulncheck:
    govulncheck ./...

# Install a dev build to GOBIN (overrides Homebrew)
install:
    go install -ldflags "{{ldflags}}" {{main}}
    @echo "Installed dev build to $(go env GOBIN)"

# Remove the dev build (switch back to Homebrew)
uninstall:
    rm -f "$(go env GOBIN)/{{bin}}"
    @echo "Removed dev build."

# Show which ucmix binary is active
which:
    @which {{bin}}
    @{{bin}} --version

# Run the CLI without building (e.g. just run dump)
run *args:
    go run {{main}} {{args}}

# Remove build artifacts
clean:
    rm -rf dist completions

# Show current version
version:
    go run {{main}} --version

# Dry-run release (validate GoReleaser config locally)
release-dry-run:
    goreleaser release --snapshot --clean

# Tag and push a release (e.g. just release v0.1.0)
release tag:
    git tag {{tag}}
    git push origin {{tag}}
