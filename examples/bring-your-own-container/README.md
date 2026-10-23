# Bring Your Own Container

Run a sandbox with a **custom** image (OpenShell-style BYOC). quarshell does not require
its first-party agent layers — any standard Linux image works if it meets the
contract in the [image reference](https://klorq.github.io/sandbox.dev/reference/images/).

## Quick start

```bash
docker build -t quarshell-byoc:latest quarshell-cli/examples/bring-your-own-container
docker tag quarshell-byoc:latest registry.example.com/team/quarshell-byoc:latest
docker push registry.example.com/team/quarshell-byoc:latest

quarshell sandbox create --name byoc \
  --image registry.example.com/team/quarshell-byoc:latest \
  --upload quarshell-cli/examples/bring-your-own-container/app.py
quarshell sandbox exec byoc -- python /workspace/app.py

quarshell sandbox exec byoc -- curl -sf http://127.0.0.1:8080/hello
quarshell sandbox rm byoc
```

Or register an alias:

```yaml
# ~/.config/quarshell/config.yaml
images:
  byoc: registry.example.com/team/quarshell-byoc:latest
```

```bash
quarshell sandbox create --name byoc --from byoc --upload ./app.py --policy quarshell-cli/policies/default.yaml
quarshell sandbox exec byoc -- python /workspace/app.py
```

## Requirements

| Rule | Why |
|------|-----|
| Non-root `USER` (or policy `run_as_*`) | Matches Docker/Podman sandbox identity |
| Writable `/sandbox` or `/workspace` | Agent/workdir |
| `iproute2` recommended | Netns / routing |
| No distroless / `FROM scratch` | Need a real userland |
| Pass command after `--` | Image CMD is replaced by `quarshell-init` |

First-party images (`--from cursor`, …): build locally or `task images:pull` / GHCR — [image reference](https://klorq.github.io/sandbox.dev/reference/images/).
