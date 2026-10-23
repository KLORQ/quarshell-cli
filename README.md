<h1 align="center">quarshell-cli</h1>

<p align="center">
  <strong>Agent sandbox CLI</strong><br>
  Create, harden, and operate policy-bound sandboxes — Cursor, Claude, Codex, and BYOC.
</p>
<p align="center">
  <a href="https://github.com/KLORQ/quarshell-cli/actions/workflows/ci.yml"><img src="https://github.com/KLORQ/quarshell-cli/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/KLORQ/quarshell-cli/releases"><img src="https://img.shields.io/github/v/release/KLORQ/quarshell-cli?include_prereleases&sort=semver&label=release" alt="release"></a>
  <a href="https://img.shields.io/badge/API-pre--stable%200.x-orange"><img src="https://img.shields.io/badge/API-pre--stable%200.x-orange" alt="pre-stable API"></a>
  <a href="https://www.apache.org/licenses/LICENSE-2.0"><img src="https://img.shields.io/badge/License-Apache--2.0-blue.svg" alt="License"></a>
  <a href="https://github.com/KLORQ/quarshell-cli"><img src="https://img.shields.io/badge/Go-1.27+-00ADD8?logo=go" alt="Go Version"></a>
</p>
<p align="center">
  <sub>Part of the <a href="https://github.com/KLORQ">Quarshell</a> ecosystem</sub>
</p>

---

## Overview

The [quick start](https://klorq.github.io/sandbox.dev/get-started/) contains the supported installation and first-sandbox flow.

**quarshell-cli** is the user-facing `cautem` binary for Quarshell: sandbox lifecycle, policy checks, provider attach, gateway selection, live logs, and agent images.

### Key Features

| Category | Capabilities |
|----------|--------------|
| **Sandboxes** | create / exec / connect / rm — Docker-first via `quarshell-driver` |
| **Policy** | YAML check/compose with `quarshell-core` + provider presets |
| **Agents** | `--from cursor\|claude\|codex\|base\|gui\|gpu\|gui-gpu` (GHCR or local) |
| **Providers** | Cursor, GitHub, NVIDIA, local inference (`host.cautem.internal`) |
| **Observability** | `cautem logs --tail`, TUI (`cautem term`), OCSF audit lines |
| **Gateway** | register with `quarshell-gateway`, relay exec, policy proposals |

---

## Installation

Install the latest CLI release into `~/.local/bin`; Linux and macOS archives include the Gateway and runtime helpers, while the current Windows archive includes the CLI:

```bash
curl -LsSf https://raw.githubusercontent.com/KLORQ/quarshell-cli/main/install.sh | sh
```

Pin a release with `CAUTEM_VERSION=vX.Y.Z`, or skip gateway startup with `CAUTEM_NO_START=1`.

From source in the [Quarshell workspace](https://github.com/KLORQ/quarshell-workspace):

```bash
git clone https://github.com/KLORQ/quarshell-workspace.git
cd quarshell-workspace
tools/checkout-modules.sh
task workspace:sync
go build -C quarshell-cli -o cautem ./cmd/cautem
```

**Requirements:** Docker or Podman. Go 1.27+ only if building from source.

Quarshell APIs are pre-stable and remain in the `0.x` series. OpenShell behavioral compatibility is still partial. See [development and releases](https://klorq.github.io/sandbox.dev/reference/development/) and the [compatibility status](https://klorq.github.io/sandbox.dev/reference/openshell-compatibility/).

---

## Quick Start

```bash
./cautem status
./cautem policy check ./policies/default.yaml

./cautem sandbox create --name demo --gateway-workspace default --upload "$PWD" --policy ./policies/default.yaml
./cautem sandbox exec demo -- uname -a
./cautem sandbox rm demo
```

Create on the selected Gateway compute backend with a published OCI image or a
workspace-scoped shared template:

```bash
./cautem sandbox template create shared-agent --gateway \
  --image ghcr.io/example/agent:stable --cpu 2 --memory 4Gi --env MODE=fast
./cautem sandbox create --gateway-workspace team \
  --name agent --image ghcr.io/example/agent:stable \
  --cpu 500m --memory 2Gi --gpu 1 --env MODE=fast
```

Sandbox creation is Gateway managed and follows the OpenShell CreateSandbox
contract. Image creates accept CPU, memory, GPU count, environment, labels,
providers, policy, restart policy and per-driver config overrides. A shared
template owns its workload settings and rejects inline workload overrides.
Local bind mounts/builds, display, and local runtime switches are not part of
this create path. `--upload` streams through OpenShell interactive execution.

### Cursor agent

```bash
./cautem sandbox create --name cursor --from cursor --upload "$PWD" \
  --provider cursor --provider gh --policy ./policies/cursor.yaml
./cautem sandbox exec cursor -- agent
```

Agent Dockerfiles live in [`docker/agents/`](./docker/agents/). Policies: [`policies/`](./policies/).
Runnable recipes live in [`examples/`](./examples/).

---

## Package Structure

| Path | Purpose |
|------|---------|
| `cmd/cautem` | CLI entrypoint |
| `internal/app` | Commands (sandbox, proxy, gateway, rules, …) |
| `policies/` | Builtin policy YAML |
| `docker/agents/` | cursor / claude / codex images |


---

## Related

| Resource | Link |
|----------|------|
| Roadmap | [ROADMAP.md](./ROADMAP.md) |
| Organization | [https://github.com/KLORQ](https://github.com/KLORQ) |
| Organization overview | [github.com/KLORQ](https://github.com/KLORQ) |
| CLI source | [`cmd/cautem/`](./cmd/cautem/) |

## License

[Apache-2.0](./LICENSE) © cautem
