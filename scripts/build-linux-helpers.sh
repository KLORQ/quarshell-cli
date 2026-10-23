#!/usr/bin/env bash
# SPDX-FileCopyrightText: Copyright (c) 2026 the quarshell authors
# SPDX-License-Identifier: Apache-2.0
#
# Cross-compile the linux helpers every release archive ships under
# libexec/quarshell/linux-<arch>/ so installs never need Go or a checkout:
#   quarshell       — proxy sidecar / supervisor relay (linux build of the CLI)
#   quarshell-init  — guest init / harden
#   quarshell-sshd  — guest sshd on a Unix socket (IDE / connect)
#
# Usage: scripts/build-linux-helpers.sh VERSION [OUT_DIR] [ARCH...]
# Run from quarshell-cli with ../quarshell-runtime checked out (GOWORK set).
set -euo pipefail

version="${1:?usage: build-linux-helpers.sh VERSION [OUT_DIR] [ARCH...]}"
out="${2:-build/helpers}"
shift $(( $# >= 2 ? 2 : 1 ))
arches=("$@")
[[ ${#arches[@]} -gt 0 ]] || arches=(amd64 arm64)

cli_dir="$(cd "$(dirname "$0")/.." && pwd)"
runtime_dir="${QUARSHELL_RUNTIME_DIR:-${cli_dir}/../quarshell-runtime}"
[[ -f "${runtime_dir}/go.mod" ]] || { echo "build-linux-helpers: ${runtime_dir} is not quarshell-runtime" >&2; exit 1; }
mkdir -p "$out"
out="$(cd "$out" && pwd)"

ldflags="-s -w -X github.com/KLORQ/quarshell-cli/internal/service.BuildVersion=${version}"
for arch in "${arches[@]}"; do
  dst="${out}/linux-${arch}"
  rm -rf "$dst"
  mkdir -p "$dst"
  echo "→ linux/${arch} helpers → ${dst}"
  export GOOS=linux GOARCH="$arch" CGO_ENABLED=0
  go build -C "$cli_dir" -trimpath -ldflags="$ldflags" -o "${dst}/quarshell" ./cmd/quarshell
  go build -C "$runtime_dir" -trimpath -ldflags="-s -w" -o "${dst}/quarshell-init" ./cmd/quarshell-init
  go build -C "$runtime_dir" -trimpath -ldflags="-s -w" -o "${dst}/quarshell-sshd" ./cmd/quarshell-sshd
  chmod 0755 "${dst}"/*
done
