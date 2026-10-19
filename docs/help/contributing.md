---
description: "How to build W-UI from source, run its tests, and send a change."
---

# Contributing

## Build

```bash
git clone https://github.com/AbolfazlTafakori/w-ui
cd w-ui/web && npm ci && npx vite build && cd ..
CGO_ENABLED=0 go build -o wui ./cmd/wui
```

The frontend is embedded into the binary, so build it first. You need the Go version named in `go.mod` and Node 22 or later.

## Run locally

```bash
WUI_DATA_DIR=./data WUI_LISTEN=127.0.0.1:2096 ./wui
```

On a machine without nftables, the panel runs with enforcement reported as unavailable: every page works, and limits are not applied.

For the frontend with hot reload, `cd web && npm run dev` proxies `/api` to the binary.

## Test

```bash
go vet ./... && go test ./...
bash scripts/test-install-questions.sh install.sh
bash scripts/test-install-output.sh install.sh
```

CI runs those, plus `staticcheck`, `govulncheck`, a frontend build that must match the committed bundle, and a clean install on eight distribution images.

## Send a change

Open a pull request against `main` saying what changed, why, and how it was tested. Keep the commit message in the style of the log: a sentence about what the change does for the operator.

## Documentation

This site is `docs/`: VitePress, with English at the root and Persian under `fa/`. Run `cd docs && npm ci && npm run dev`. Every page exists in both languages, so add both; a test fails when one is missing or not in the sidebar.
