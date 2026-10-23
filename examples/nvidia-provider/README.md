# NVIDIA provider profile example

Builtin profile: `quarshell-providers/profiles/nvidia.yaml` (also via `cautem provider profile show nvidia`).

**Запуск + create:** [профили провайдеров](https://klorq.github.io/sandbox.dev/ru/guides/provider-profiles/).

```bash
export GOWORK=$PWD/go.work
go build -C quarshell-cli -o cautem ./cmd/cautem && ./cautem install
go build -C quarshell-gateway -o quarshell-gateway ./cmd/quarshell-gateway
./quarshell-gateway --listen 127.0.0.1:7443 &
cautem gateway add http://127.0.0.1:7443 --local --name local && cautem gateway select local

NVIDIA_API_KEY=… cautem provider create --name nv --type nvidia --credential NVIDIA_API_KEY

cautem sandbox create --name nim \
  --policy quarshell-cli/policies/default.yaml \
  --gateway http://127.0.0.1:7443 \
  --provider nv

cautem provider effective nim
cautem sandbox exec nim -- env | grep NVIDIA || true
# expect: NVIDIA_API_KEY=cautem:resolve:env:NVIDIA_API_KEY
```

См. [профили провайдеров](https://klorq.github.io/sandbox.dev/ru/guides/provider-profiles/).
