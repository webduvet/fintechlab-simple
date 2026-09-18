#!/bin/sh
# Run the Go toolchain in a container: this host has no local `go`.
# Module cache is shared with the host's ~/go/pkg so repeat runs are fast.
exec podman run --rm -i \
  -v "$(pwd)":/src:z \
  -v "$HOME/go/pkg":/go/pkg:z \
  -w /src \
  -e GOFLAGS=-buildvcs=false \
  -e HOME=/tmp \
  --userns=keep-id \
  docker.io/library/golang:1.22-alpine go "$@"
