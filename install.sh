#!/bin/sh
# SPDX-FileCopyrightText: Copyright (c) 2026 the quarshell authors
# SPDX-License-Identifier: Apache-2.0
#
# Install the Quarshell CLI and, where shipped, its local Gateway.
#
# Usage:
#   curl -LsSf https://raw.githubusercontent.com/KLORQ/quarshell-cli/main/install.sh | sh
#
# Environment:
#   QUARSHELL_VERSION       Release tag (default: latest release)
#   QUARSHELL_INSTALL_DIR   Install directory (default: ~/.local/bin)
#   QUARSHELL_REPO          Override owner/name (default: KLORQ/quarshell-cli)
#   QUARSHELL_RELEASE_URL   Base URL holding <tag>/<archive> and checksums.txt
#   QUARSHELL_NO_START      Set to 1 to skip starting the local gateway
set -eu

APP_NAME="quarshell"
REPO="${QUARSHELL_REPO:-KLORQ/quarshell-cli}"
GITHUB_URL="https://github.com/$REPO"
API_URL="https://api.github.com/repos/$REPO"

info() { printf '%s: %s\n' "$APP_NAME" "$*" >&2; }
error() { printf '%s: error: %s\n' "$APP_NAME" "$*" >&2; exit 1; }
has_cmd() { command -v "$1" >/dev/null 2>&1; }
require_cmd() { has_cmd "$1" || error "'$1' is required"; }

download() {
  _url="$1"; _out="$2"
  if has_cmd curl; then
    curl -fLsS --retry 3 --max-redirs 5 -o "$_out" "$_url"
  elif has_cmd wget; then
    wget -q -O "$_out" "$_url"
  else
    error "need curl or wget"
  fi
}

detect_archive() {
  _os="$(uname -s)"; _arch="$(uname -m)"
  case "$_os" in
    Linux) _os_name="Linux" ;;
    Darwin) _os_name="Darwin" ;;
    MINGW*|MSYS*|CYGWIN*) _os_name="Windows" ;;
    *) error "unsupported OS: $_os" ;;
  esac
  case "$_arch" in
    x86_64|amd64) _arch_name="x86_64" ;;
    aarch64|arm64) _arch_name="arm64" ;;
    *) error "unsupported architecture: $_arch" ;;
  esac
  if [ "$_os_name" = "Windows" ]; then
    [ "$_arch_name" = "x86_64" ] || error "Windows arm64 packages are not published"
    printf 'quarshell_%s_%s.zip\n' "$_os_name" "$_arch_name"
  else
    printf 'quarshell_%s_%s.tar.gz\n' "$_os_name" "$_arch_name"
  fi
}

resolve_version() {
  if [ -n "${QUARSHELL_VERSION:-}" ]; then printf '%s\n' "$QUARSHELL_VERSION"; return; fi
  _json="$(mktemp)"
  download "$API_URL/releases/latest" "$_json" 2>/dev/null || error "cannot list releases from $REPO"
  _tag="$(sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' "$_json" | head -1)"
  rm -f "$_json"
  [ -n "$_tag" ] || error "no releases found on $REPO"
  printf '%s\n' "$_tag"
}

install_dir() {
  if [ -n "${QUARSHELL_INSTALL_DIR:-}" ]; then printf '%s\n' "$QUARSHELL_INSTALL_DIR"; return; fi
  printf '%s/.local/bin\n' "$HOME"
}

verify_checksum() {
  _name="$1"; _file="$2"; _sums="$3"
  [ -s "$_sums" ] || error "checksums.txt is missing"
  _expected="$(awk -v name="$_name" '$2 == name || $2 == "*" name { print $1; exit }' "$_sums")"
  [ -n "$_expected" ] || error "$_name is not listed in checksums.txt"
  if has_cmd sha256sum; then
    _actual="$(sha256sum "$_file" | awk '{print $1}')"
  elif has_cmd shasum; then
    _actual="$(shasum -a 256 "$_file" | awk '{print $1}')"
  else
    error "sha256sum or shasum is required to verify the release"
  fi
  [ "$_actual" = "$_expected" ] || error "checksum mismatch for $_name"
}

main() {
  require_cmd uname; require_cmd sed; require_cmd mktemp; require_cmd awk; require_cmd install; require_cmd cp
  _version="$(resolve_version)"
  _archive="$(detect_archive)"
  _base="${QUARSHELL_RELEASE_URL:-$GITHUB_URL/releases/download}/$_version"
  _dir="$(install_dir)"
  _tmp="$(mktemp -d)"
  trap 'rm -rf "$_tmp"' EXIT HUP INT TERM

  info "installing $_version from $REPO -> $_dir"
  download "$_base/$_archive" "$_tmp/$_archive" || error "download failed: $_base/$_archive"
  download "$_base/checksums.txt" "$_tmp/checksums.txt" || error "download failed: $_base/checksums.txt"
  verify_checksum "$_archive" "$_tmp/$_archive" "$_tmp/checksums.txt"

  _payload="$_tmp/payload"
  mkdir -p "$_payload"
  case "$_archive" in
    *.zip) require_cmd unzip; unzip -q "$_tmp/$_archive" -d "$_payload" ;;
    *.tar.gz) require_cmd tar; tar -xzf "$_tmp/$_archive" -C "$_payload" ;;
    *) error "unsupported release archive: $_archive" ;;
  esac

  _exe=""
  case "$_archive" in *_Windows_*.zip) _exe=".exe" ;; esac
  [ -s "$_payload/quarshell$_exe" ] || error "archive does not contain quarshell$_exe"
  mkdir -p "$_dir" 2>/dev/null || true
  _sudo=""
  if [ ! -w "$_dir" ]; then _sudo="sudo"; sudo mkdir -p "$_dir"; fi
  $_sudo install -m 755 "$_payload/quarshell$_exe" "$_dir/quarshell$_exe"

  if [ -s "$_payload/quarshell-gateway$_exe" ]; then
    $_sudo install -m 755 "$_payload/quarshell-gateway$_exe" "$_dir/quarshell-gateway$_exe"
    info "installed $_dir/quarshell-gateway$_exe"
  else
    info "this platform release does not include the Gateway binary"
  fi
  if [ -d "$_payload/libexec/quarshell" ]; then
    _prefix_dir=$(dirname "$_dir")
    for _helper in "$_payload"/libexec/quarshell/*/*; do
      [ -f "$_helper" ] || continue
      _relative=${_helper#"$_payload"/}
      _helper_dir=$_prefix_dir/$(dirname "$_relative")
      $_sudo mkdir -p "$_helper_dir"
      $_sudo install -m 755 "$_helper" "$_prefix_dir/$_relative"
    done
  fi
  if [ -d "$_payload/providers" ]; then
    $_sudo mkdir -p "$_dir/providers"
    $_sudo cp -R "$_payload/providers/." "$_dir/providers/"
  fi

  info "installed $_dir/quarshell$_exe"
  case ":$PATH:" in
    *:"$_dir":*) ;;
    *) info "$_dir is not on PATH; add: export PATH=\"$_dir:\$PATH\"" ;;
  esac
  if [ "$_exe" = ".exe" ]; then
    info "run quarshell from Git Bash; the local Gateway binary is not included on Windows"
  elif [ "${QUARSHELL_NO_START:-0}" != "1" ] && [ -x "$_dir/quarshell-gateway" ]; then
    info "starting local gateway"
    if PATH="$_dir:$PATH" "$_dir/quarshell" gateway ensure; then
      PATH="$_dir:$PATH" "$_dir/quarshell" status || true
    else
      info "gateway did not start; run: $_dir/quarshell gateway ensure"
    fi
  fi
  info "next: quarshell sandbox create --name demo"
}

main "$@"
