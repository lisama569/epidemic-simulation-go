# 情景配置示例

- `v2_grouped.json`：当前版本（schema_version=2）分群 SEIR，含分阶段接触减少、
  容量受限疫苗接种与隔离。`episim run/gen/estimate/determinism -f` 均可用。
- `v2_metapop.json`：当前版本集合种群 SIR，三斑块迁移矩阵。
- `v1.json`：旧版（version="1"）配置，运行时按 README 的确定性规则归一为 v2。
- `bad.json`：故意矛盾的配置（群初始仓室不闭合），用于验证被明确拒绝：

```
$ go run ./cmd/episim run -f testdata/bad.json
REJECTED [group_not_closed]: configuration invalid: group "a" initial ...
```
