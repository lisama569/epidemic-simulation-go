#!/usr/bin/env bash
# 本地一键验证：格式化、静态检查、竞态测试、CLI 端到端冒烟。
# 不依赖网络或外部数据。
set -euo pipefail
cd "$(dirname "$0")"

echo "== gofmt =="
test -z "$(gofmt -l .)"

echo "== go vet =="
go vet ./...

echo "== go test -race -count=1 =="
go test -race -count=1 ./...

echo "== CLI: run (v2 / v1 归一 / v2+接种) =="
go run ./cmd/epidemic run -config testdata/scenario-v2.json > /dev/null
go run ./cmd/epidemic run -config testdata/scenario-v1-legacy.json > /dev/null
go run ./cmd/epidemic run -config testdata/scenario-v2-vaccine.json > /dev/null

echo "== CLI: estimate + demo =="
go run ./cmd/epidemic estimate -config testdata/scenario-v2.json -parallelism 4 > /dev/null
go run ./cmd/epidemic demo > /dev/null

echo "ALL VERIFICATION PASSED"
