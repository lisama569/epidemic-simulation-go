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

echo "== CLI: run (v2 / v1 归一) =="
go run ./cmd/epidemic run -config testdata/scenario-v2.json > /dev/null
go run ./cmd/epidemic run -config testdata/scenario-v1-legacy.json > /dev/null

echo "== CLI: estimate + demo =="
go run ./cmd/epidemic estimate -config testdata/scenario-v2.json -parallelism 4 > /dev/null
go run ./cmd/epidemic demo > /dev/null

echo "== CLI: 接种情景断点续跑（中断 -> 续跑 -> 与不中断比对） =="
SIMCP="$(mktemp -d)/simcp"
go run ./cmd/epidemic run -config testdata/scenario-v2-vaccination.json \
  -checkpoint-dir "$SIMCP" -stop-after-steps 20 > /tmp/epi-part.json && exit 1 || true
go run ./cmd/epidemic run -config testdata/scenario-v2-vaccination.json \
  -checkpoint-dir "$SIMCP" > /tmp/epi-resumed.json
go run ./cmd/epidemic run -config testdata/scenario-v2-vaccination.json > /tmp/epi-full.json
python3 - <<'PYEOF'
import json
full = json.load(open('/tmp/epi-full.json'))
res = json.load(open('/tmp/epi-resumed.json'))
for k in ('steps', 'peakInfected', 'finalRemoved', 'finalVaccinated',
          'capacityEvents', 'vaccinationEvents'):
    assert full[k] == res[k], f"字段 {k} 续跑后与不中断运行不一致"
print("断点续跑输出与不中断运行逐字段一致")
PYEOF

echo "ALL VERIFICATION PASSED"
