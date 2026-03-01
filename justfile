binary := "ralfinho"
version := `git describe --tags --always --dirty 2>/dev/null || echo dev`
ldflags := "-X github.com/fsmiamoto/ralfinho/internal/cli.Version=" + version

# Show available recipes
help:
    @just --list

# Build the CLI into ./bin
build:
    mkdir -p bin
    go build -ldflags '{{ldflags}}' -o bin/{{binary}} ./cmd/{{binary}}

# Run the CLI locally (pass args after --, e.g. just run -- --help)
run *args:
    go run ./cmd/{{binary}} {{args}}

# Format all Go files
fmt:
    gofmt -w $(find . -name '*.go' -not -path './bin/*')

# Lint checks (Go vet)
lint:
    go vet ./...

# Run all tests
test:
    go test ./...

# Install the CLI with Go into $GOBIN (or $GOPATH/bin)
install: build
    go install -ldflags '{{ldflags}}' ./cmd/{{binary}}

# Remove local build artifacts
clean:
    rm -f bin/{{binary}}

# Tag the latest commit and push the tag (e.g. just tag v1.0.0)
tag version:
    git tag {{version}}
    git push origin {{version}}
