#!/bin/sh
# SPDX-FileCopyrightText: Copyright (c) 2026 the cautem authors
# SPDX-License-Identifier: Apache-2.0
#
# Install cautem and the local gateway from a KLORQ/quarshell release.
#
# Usage:
#   curl -LsSf https://raw.githubusercontent.com/KLORQ/quarshell/master/install.sh | sh
#
# Environment:
#   CAUTEM_VERSION       Release tag (default: latest published release)
#   CAUTEM_INSTALL_DIR   Install directory (default: ~/.local/bin)
#   CAUTEM_REPO          Override owner/name (default: KLORQ/quarshell)
#   CAUTEM_RELEASE_URL   Base URL holding <tag>/<asset> (mirror / air-gapped)
#   CAUTEM_NO_START      Set to 1 to skip starting the local gateway
set -eu

APP_NAME="cautem"
REPO="${CAUTEM_REPO:-KLORQ/quarshell}"
GITHUB_URL="https://github.com/${REPO}"
API_URL="https://api.github.com/repos/${REPO}"

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

detect_target() {
  _os="$(uname -s)"; _arch="$(uname -m)"
  case "$_os" in
    Linux) _goos="linux" ;;
    Darwin) _goos="darwin" ;;
    MINGW*|MSYS*|CYGWIN*) _goos="windows" ;;
    *) error "unsupported OS: $_os" ;;
  esac
  case "$_arch" in
    x86_64|amd64) _goarch="amd64" ;;
    aarch64|arm64) _goarch="arm64" ;;
    *) error "unsupported arch: $_arch" ;;
  esac
  printf '%s-%s\n' "$_goos" "$_goarch"
}

resolve_version() {
  if [ -n "${CAUTEM_VERSION:-}" ]; then printf '%s\n' "$CAUTEM_VERSION"; return; fi
  _json="$(mktemp)"
  download "${API_URL}/releases/latest" "$_json" 2>/dev/null || error "cannot list releases"
  _tag="$(sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' "$_json" | head -1)"
  rm -f "$_json"
  [ -n "$_tag" ] || error "no releases found on ${REPO}"
  printf '%s\n' "$_tag"
}

install_dir() {
  if [ -n "${CAUTEM_INSTALL_DIR:-}" ]; then printf '%s\n' "$CAUTEM_INSTALL_DIR"; return; fi
  printf '%s\n' "${HOME}/.local/bin"
}

verify_checksum() {
  _name="$1"; _file="$2"; _sums="$3"
  if [ ! -s "$_sums" ]; then info "SHA256SUMS not found; skipping verify"; return; fi
  _expected="$(grep "[ *]${_name}\$" "$_sums" | awk '{print $1}' | head -1)"
  [ -n "$_expected" ] || error "${_name} missing from SHA256SUMS"
  if has_cmd sha256sum; then _actual="$(sha256sum "$_file" | awk '{print $1}')";
  elif has_cmd shasum; then _actual="$(shasum -a 256 "$_file" | awk '{print $1}')";
  else info "sha256 tool missing; skipping verify"; return; fi
  [ "$_actual" = "$_expected" ] || error "checksum mismatch for ${_name}"
}

main() {
  require_cmd uname; require_cmd sed; require_cmd mktemp
  _version="$(resolve_version)"
  _target="$(detect_target)"
  _exe=""; case "$_target" in windows-*) _exe=".exe" ;; esac
  _cli="quarshell-cli-${_target}${_exe}"
  _gateway="quarshell-gateway-${_target}${_exe}"
  _prover="quarshell-prover-${_target}${_exe}"
  _base="${CAUTEM_RELEASE_URL:-${GITHUB_URL}/releases/download}/${_version}"
  _dir="$(install_dir)"
  _tmp="$(mktemp -d)"
  trap 'rm -rf "$_tmp"' EXIT

  info "installing ${_version} (${_target}) -> ${_dir}"
  download "${_base}/${_cli}" "${_tmp}/${_cli}" || error "download failed: ${_base}/${_cli}"
  download "${_base}/${_gateway}" "${_tmp}/${_gateway}" || error "download failed: ${_base}/${_gateway}"
  download "${_base}/SHA256SUMS" "${_tmp}/SHA256SUMS" 2>/dev/null || true
  verify_checksum "$_cli" "${_tmp}/${_cli}" "${_tmp}/SHA256SUMS"
  verify_checksum "$_gateway" "${_tmp}/${_gateway}" "${_tmp}/SHA256SUMS"
  if download "${_base}/${_prover}" "${_tmp}/${_prover}" 2>/dev/null; then
    verify_checksum "$_prover" "${_tmp}/${_prover}" "${_tmp}/SHA256SUMS"
  else
    info "prover asset not published for ${_version}; skipping"
  fi

  _sudo=""
  mkdir -p "$_dir" 2>/dev/null || true
  if [ ! -w "$_dir" ]; then _sudo="sudo"; sudo mkdir -p "$_dir"; fi
  $_sudo install -m 755 "${_tmp}/${_cli}" "${_dir}/${APP_NAME}${_exe}"
  $_sudo install -m 755 "${_tmp}/${_gateway}" "${_dir}/quarshell-gateway${_exe}"
  if [ -f "${_tmp}/${_prover}" ]; then
    $_sudo install -m 755 "${_tmp}/${_prover}" "${_dir}/quarshell-prover${_exe}"
    info "installed ${_dir}/quarshell-prover${_exe}"
  fi
  info "installed ${_dir}/${APP_NAME}${_exe}"
  info "installed ${_dir}/quarshell-gateway${_exe}"

  case ":${PATH}:" in
    *:"$_dir":*) ;;
    *) info "${_dir} is not on PATH; add: export PATH=\"${_dir}:\$PATH\"" ;;
  esac

  if ! has_cmd docker && ! has_cmd podman; then
    info "note: container sandboxes need Docker or Podman; VM mode does not"
  fi
  if [ "${CAUTEM_NO_START:-0}" != "1" ]; then
    info "starting local gateway"
    if PATH="${_dir}:${PATH}" "${_dir}/${APP_NAME}${_exe}" gateway ensure; then
      info "gateway status:"
      PATH="${_dir}:${PATH}" "${_dir}/${APP_NAME}${_exe}" status || true
    else
      info "gateway did not start; run: ${_dir}/${APP_NAME}${_exe} gateway ensure"
    fi
  fi
  info "next: ${APP_NAME} sandbox create --name demo"
}

main "$@"
