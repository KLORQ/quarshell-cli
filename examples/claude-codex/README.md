# Claude / Codex-style agents

Use builtin Anthropic + OpenAI presets **or** a `claude-code` provider instance.
API keys stay on the host / gateway; the sandbox sees `quarshell:resolve:env:…` placeholders.

**Запуск + providers:** [профили провайдеров](https://klorq.github.io/sandbox.dev/ru/guides/provider-profiles/).

## A. Inference presets in policy (no gateway provider)

```bash
export GOWORK=$PWD/go.work
# keys on host for rewrite when using local proxy path — prefer gateway provider (B)

quarshell sandbox create --name agent-demo \
  --policy quarshell-cli/examples/claude-codex/policy.yaml
```

## B. Gateway provider (рекомендуется)

```bash
quarshell-gateway --listen 127.0.0.1:7443 &
quarshell gateway add http://127.0.0.1:7443 --local --name local && quarshell gateway select local

ANTHROPIC_API_KEY=… quarshell provider create --name claude --type claude-code --credential ANTHROPIC_API_KEY

quarshell sandbox create --name agent-demo \
  --policy quarshell-cli/examples/claude-codex/policy.yaml \
  --gateway http://127.0.0.1:7443 \
  --provider claude

quarshell sandbox exec agent-demo -- env | grep -E 'ANTHROPIC|OPENAI'
# expect: ANTHROPIC_API_KEY=quarshell:resolve:env:ANTHROPIC_API_KEY
```

Custom host model (vLLM): `inference.profiles` — see [provider profiles](https://klorq.github.io/sandbox.dev/guides/provider-profiles/).
