# NVIDIA provider profile example

Builtin profile: `quarshell-policy/profiles/nvidia.yaml` (also via `quarshell provider profile show nvidia`).

**Запуск + create:** [профили провайдеров](https://klorq.github.io/sandbox.dev/ru/guides/provider-profiles/).

```bash
export GOWORK=$PWD/go.work
go build -C quarshell-cli -o quarshell ./cmd/quarshell && ./quarshell install
go build -C quarshell-gateway -o quarshell-gateway ./cmd/quarshell-gateway
./quarshell-gateway --listen 127.0.0.1:7443 &
quarshell gateway add http://127.0.0.1:7443 --local --name local && quarshell gateway select local

NVIDIA_API_KEY=… quarshell provider create --name nv --type nvidia --credential NVIDIA_API_KEY

quarshell sandbox create --name nim \
  --policy quarshell-cli/policies/default.yaml \
  --gateway http://127.0.0.1:7443 \
  --provider nv

quarshell provider effective nim
quarshell sandbox exec nim -- env | grep NVIDIA || true
# expect: NVIDIA_API_KEY=quarshell:resolve:env:NVIDIA_API_KEY
```

См. [профили провайдеров](https://klorq.github.io/sandbox.dev/ru/guides/provider-profiles/).
