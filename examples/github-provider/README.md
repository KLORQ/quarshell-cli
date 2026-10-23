# GitHub provider — agent push inside a sandbox

Goal: an agent **inside** the sandbox can `git push` / `gh` to your org.
The host only seeds the token once; the guest sees `quarshell:resolve:env:GITHUB_TOKEN`,
and the sidecar rewrites it on egress.

**Общий запуск + create:** [профили провайдеров](https://klorq.github.io/sandbox.dev/ru/guides/provider-profiles/)\
**Cursor+Git:** [Docker](https://klorq.github.io/sandbox.dev/ru/providers/docker/).

## Prerequisites

- Docker (or Podman) running on the host
- Go toolchain + this workspace (`go.work`)
- GitHub PAT with **`repo`** (classic) or fine-grained **Contents: Read and write**
  on the target repos
- Empty (or existing) repos under the org, e.g. `quarshell/quarshell-cli`

## 1. Build CLI + gateway

```bash
cd /path/to/workspace
export GOWORK=$PWD/go.work
go build -C quarshell-cli -o ../quarshell ./cmd/quarshell
go build -C quarshell-gateway -o ../quarshell-gateway ./cmd/quarshell-gateway
./quarshell install   # or: export PATH="$PWD:$PATH"
```

## 2. Start gateway + register it

```bash
./quarshell-gateway --listen 127.0.0.1:7443 &
./quarshell gateway add http://127.0.0.1:7443 --local --name local
./quarshell gateway select local
```

## 3. Seed GitHub token (one-shot — not a sticky shell export)

```bash
# PAT only for this process; do not leave export in your interactive shell
GITHUB_TOKEN=ghp_… ./quarshell provider create --name gh --type github --credential GITHUB_TOKEN
./quarshell provider list   # shows name/type/keys only — never the value
```

## 4. Create a sandbox that can push

Use the write-capable **base policy** from the start (org-scoped `quarshell`):

```bash
./quarshell sandbox create --name push \
  --upload "$PWD" \
  --policy quarshell-cli/policies/github-push-quarshell.yaml \
  --gateway http://127.0.0.1:7443 \
  --provider gh
# If instance missing: use --provider github (auto-creates from profile + $GITHUB_TOKEN)
# or: GITHUB_TOKEN=… ./quarshell provider create --name gh --type github --credential GITHUB_TOKEN
```

Composition: **base** = push policy (`git-receive-pack` + API write under `/quarshell/**`)
plus **provider-composed** github endpoints/credential binding from `gh`.

Check:

```bash
./quarshell policy get push --base    # editable base
./quarshell policy get push --full    # effective policy (base + provider-composed)
```

### Alternative: create narrow, then widen

```bash
./quarshell sandbox create --name push --upload "$PWD" \
  --policy quarshell-cli/policies/default.yaml \
  --gateway http://127.0.0.1:7443 --provider gh

# push will DENY until:
./quarshell policy set push --policy quarshell-cli/policies/github-push-quarshell.yaml
```

## 5. Run the agent *inside* the sandbox

Do **not** push from the host with a placeholder token. Examples:

```bash
# one-shot command
./quarshell sandbox exec push -- git -C /workspace/quarshell-cli status

# interactive shell (then run your agent / git / gh)
./quarshell sandbox exec push -- bash

# create the sandbox, then run the agent with native Gateway exec
./quarshell sandbox create --name agent --upload "$PWD" \
  --policy quarshell-cli/policies/github-push-quarshell.yaml \
  --gateway http://127.0.0.1:7443 --provider gh
./quarshell sandbox exec agent -- bash
```

Inside the guest, env looks like:

```text
GITHUB_TOKEN=quarshell:resolve:env:GITHUB_TOKEN
```

`git` / `gh` / `curl` to `github.com` / `api.github.com` go through the sidecar;
the real PAT is substituted only for credential-bound endpoints.

### First push per module

Repos must exist on GitHub. From **inside** the sandbox:

```bash
cd /workspace/quarshell-cli
git remote -v   # should be https://github.com/KLORQ/quarshell-cli.git
git push -u origin main
```

Repeat for `quarshell-core`, `quarshell-runtime`, `quarshell-gateway` as needed.

## 6. If push is denied

```bash
./quarshell logs push --source proxy | grep -E 'DENIED|FINDING'
./quarshell policy get push --full | head
# fix base, then:
./quarshell policy set push --policy quarshell-cli/policies/github-push-quarshell.yaml
```

Typical causes:

| Symptom | Fix |
|---------|-----|
| DENY `git-receive-pack` | base missing push rules → `policy set` push YAML |
| `credential_endpoint_mismatch` | provider not attached / wrong key binding |
| GitHub 401 | re-seed: `GITHUB_TOKEN=… quarshell provider update gh --credential GITHUB_TOKEN` |
| Host push with placeholder | always `quarshell sandbox exec …` — host has no MITM rewrite |

## 7. Cleanup

```bash
./quarshell sandbox delete push
# optional: stop gateway job
```

## How policies work

quarshell starts **default deny**. Widen the base policy when the agent needs more.

```bash
./quarshell gateway ensure   # starts quarshell-gateway beside CLI if needed
./quarshell sandbox create --from cursor --upload "$PWD" --provider cursor
./quarshell sandbox exec cursor -- agent
# --provider cursor attaches the provider; gateway ensure runs automatically
```

| Layer | Builtin `github` provider | After base widen (`github-push-quarshell.yaml`) |
|-------|---------------------------|-----------------------------------------------|
| API | `read-only` | write under `/repos/quarshell/**` + **create repo** |
| Git | `git-upload-pack` (clone/fetch) | + `git-receive-pack` (push) |

Typical loop:

1. Agent tries `gh repo create` / `git push` → proxy **DENIED**
2. Operator reads logs → updates **base policy** (`policy set`)
3. Gateway **composition** keeps provider-composed credentials/endpoints
4. Agent retries inside the sandbox

Create-repo allows in our write base policy:

- `POST /orgs/quarshell/repos` — `gh repo create quarshell/quarshell-cli`
- `POST /user/repos` — user-owned `gh repo create quarshell-cli`
- then `git-receive-pack` for push

Token still needs GitHub permission (`repo` / admin on org). Policy only admits the HTTP path; GitHub ACLs still apply.

## Agent create + push (inside sandbox)

```bash
./quarshell sandbox exec push -- bash
# inside:
gh repo create quarshell/quarshell-cli --private --source=/workspace/quarshell-cli --remote=origin --push
# or:
gh api -X POST /orgs/quarshell/repos -f name=quarshell-cli -F private=true
cd /workspace/quarshell-cli && git remote add origin https://github.com/KLORQ/quarshell-cli.git
git push -u origin main
```

If DENIED: `./quarshell logs push --source proxy` → adjust base → `./quarshell policy set push …`.
