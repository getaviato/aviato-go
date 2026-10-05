#!/usr/bin/env bash
# go vet, then golangci-lint when it is installed (on PATH or in $(go env GOPATH)/bin).
# Runs in the current directory, so it serves the SDK and the Go fixtures alike.
set -euo pipefail

go vet ./...

lint="$(command -v golangci-lint || true)"
if [[ -z "$lint" && -x "$(go env GOPATH)/bin/golangci-lint" ]]; then
    lint="$(go env GOPATH)/bin/golangci-lint"
fi
if [[ -z "$lint" ]]; then
    echo "golangci-lint is not installed; skipped (go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest)"
    exit 0
fi
config="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/.golangci.yml"
"$lint" run --config "$config" ./...
