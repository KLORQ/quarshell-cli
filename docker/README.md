# quarshell-cli/docker/

Agent sandbox images for the `quarshell` CLI (`--from cursor|claude|codex`).
Runtime base (`quarshell-sandbox:local` / GHCR `:cli`) is built from the [runtime image sources](https://github.com/KLORQ/quarshell-runtime/tree/main/images/sandbox) via `task runtime:image:cli` in the multi-repo workspace.

The Kubernetes companion uses a separate minimal image with the `quarshell proxy`
entrypoint. Build it locally from the workspace with `task docker:proxy`; the
default tag is `quarshell-proxy:local`. It runs as UID 10001, keeps its policy in
`/etc/quarshell/policy.yaml`, and writes audit files below `/tmp/quarshell-logs`.
The CLI release workflow publishes a linux/amd64 and linux/arm64 manifest at
`ghcr.io/klorq/quarshell/sandboxes/proxy:<version>` after the matching CLI
release. Kubernetes deployments should pin the published image by digest once
the runtime Pod template is enabled.

| Path | Tag | Contents |
|------|-----|----------|
| [`agents/cursor`](./agents/cursor/) | `quarshell-sandbox:cursor` | + Cursor Agent CLI under `/opt/cursor-agent` |
| [`agents/claude`](./agents/claude/) | `quarshell-sandbox:claude` | + Claude Code CLI |
| [`agents/codex`](./agents/codex/) | `quarshell-sandbox:codex` | + OpenAI Codex CLI |

Published images and BYOC: [image reference](https://klorq.github.io/sandbox.dev/reference/images/).

## Build

```bash
export GOWORK=$PWD/go.work
task runtime:image:cli          # base → quarshell-sandbox:local
task docker:agent:cursor        # → quarshell-sandbox:cursor
task docker:agent:claude
task docker:agent:codex
task docker:agent:all
task docker:proxy
```

## Use

```bash
quarshell sandbox create --name cursor \
  --from cursor \
  --policy quarshell-cli/policies/cursor.yaml
quarshell sandbox exec cursor -- agent
```
