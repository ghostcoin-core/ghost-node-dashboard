#!/usr/bin/env bash
# The merge gate: vet and test the Go module. Before the module exists it has
# nothing to check and passes.
set -euo pipefail
cd "$(dirname "$0")/.."
if [ ! -f go.mod ]; then
	echo "no Go module yet; nothing to check"
	exit 0
fi
go vet ./...
go test ./... -count=1
