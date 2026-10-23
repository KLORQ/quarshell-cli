# Roadmap — quarshell-cli

Status: pre-stable API · Current module tag: `v0.1.2` · Dependencies are listed in `go.mod`.

## This module

| ID | Item | Notes |
|----|------|-------|
| L1 | **Packaging** | Homebrew / snap / installable artifacts |
| L2 | **Agent images** | Keep cursor/claude/codex GHCR publish green |
| L3 | **Observability UX** | TUI + live logs polish |
| L4 | **Driver flags** | Surface k8s/vm when drivers leave stub |


## Release

This module follows its own pre-stable 0.x version sequence. Publish dependencies
first, update consumers to the published tags, and verify with `GOWORK=off`. Never
rewrite a published tag.
