# Bring Your Own Container

Run a sandbox with a **custom** image (OpenShell-style BYOC). cautem does not require
its first-party agent layers — any standard Linux image works if it meets the
contract in the [image reference](https://klorq.github.io/sandbox.dev/reference/images/).

## Quick start

```bash
docker build -t cautem-byoc:latest quarshell-cli/examples/bring-your-own-container
docker tag cautem-byoc:latest registry.example.com/team/cautem-byoc:latest
docker push registry.example.com/team/cautem-byoc:latest

cautem sandbox create --name byoc \
  --image registry.example.com/team/cautem-byoc:latest \
  --upload quarshell-cli/examples/bring-your-own-container/app.py
cautem sandbox exec byoc -- python /workspace/app.py

cautem sandbox exec byoc -- curl -sf http://127.0.0.1:8080/hello
cautem sandbox rm byoc
```

Or register an alias:

```yaml
# ~/.config/cautem/config.yaml
images:
  byoc: registry.example.com/team/cautem-byoc:latest
```

```bash
cautem sandbox create --name byoc --from byoc --upload ./app.py --policy quarshell-cli/policies/default.yaml
cautem sandbox exec byoc -- python /workspace/app.py
```

## Requirements

| Rule | Why |
|------|-----|
| Non-root `USER` (or policy `run_as_*`) | Matches Docker/Podman sandbox identity |
| Writable `/sandbox` or `/workspace` | Agent/workdir |
| `iproute2` recommended | Netns / routing |
| No distroless / `FROM scratch` | Need a real userland |
| Pass command after `--` | Image CMD is replaced by `cautem-init` |

First-party images (`--from cursor`, …): build locally or `task images:pull` / GHCR — [image reference](https://klorq.github.io/sandbox.dev/reference/images/).
