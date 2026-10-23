# Ollama in quarshell

Run Ollama on the host, reach it from the sandbox via `host.quarshell.internal:11434`

Policy: `filesystem_policy` / `network_policies` + `inference.providers: [local]` — see `policy.yaml`.

**Запуск CLI:** [профили провайдеров](https://klorq.github.io/sandbox.dev/ru/guides/provider-profiles/).

```bash
export GOWORK=$PWD/go.work
go build -C quarshell-cli -o ../quarshell ./cmd/quarshell && ./quarshell install
task runtime:image:cli

# host
ollama serve   # listens on 11434

quarshell sandbox create --name ollama-demo \
  --policy quarshell-cli/examples/ollama/policy.yaml

quarshell sandbox exec ollama-demo -- curl -s http://host.quarshell.internal:11434/api/tags
```

No managed URL rewrite — use the native Ollama HTTP API. See [provider profiles](https://klorq.github.io/sandbox.dev/guides/provider-profiles/).
