# 本地传染病建模与情景推演引擎

纯 Go、零外部依赖的传染病建模与情景推演引擎。所有输入均为本地生成的合成数据或
JSON 情景配置；不访问网络、不使用真实数据。相同输入在重复运行与任意并行度下
产生**逐位一致**的结果。

```
go run ./cmd/episim demo            # 内置分阶段干预 + 容量受限演示
go run ./cmd/episim demo-metapop    # 多斑块迁移演示
go run ./cmd/episim run -f sc.json  # 运行情景（支持旧版 v1 配置）
go run ./cmd/episim gen -f sc.json  # 生成种子化合成观测序列
go run ./cmd/episim estimate -f sc.json [-cp cp.json]  # beta 估计/续算
go run ./cmd/episim determinism -f sc.json             # 串行 vs 并行一致性
go run ./cmd/episim resume-demo                        # 检查点续算演示
go test ./...                                            # 全部单测
```

## 1. 建模口径与状态约定

两种建模口径实现同一个 `Dynamics` 接口，由 `paradigm` 字段选择：

| paradigm | 口径 | 结构关系 |
|---|---|---|
| `grouped` | 分群均匀混合仓室模型 | 接触矩阵 `C[a][b]`：群 a 每人每天与群 b 的接触率 |
| `metapop` | 集合种群（元种群）模型 | 迁移矩阵 `M[dst][src]`：人均每天 src→dst 流量 |

`model_type` 在统一状态向量上选择疾病过程子集：

- `sir`：S → I → R（E 恒为零）
- `seir`：S → E → I → R（可经隔离进入 Q）
- `sei`：S → E → I，无 E→R 恢复通路

**统一 SEIQR 状态向量**（每个群固定五个仓室，未使用的恒为零）：

```
[S 易感, E 潜伏, I 感染, Q 隔离, R 移除/免疫]
```

多斑块模型中，每个斑块独立保持该向量，迁移在所有仓室间同步进行。

数值方法为**固定步长、固定运算顺序**的半隐式欧拉（dt = `step_days`），
每步严格按以下顺序更新（所有流量都对来源仓室做钳制，任何仓室都不会出现负数）：

1. 感染（S→E 或 S→I；感染力 `λ_a = β Σ_b C[a][b] I_b/N_b`）
2. 发病（E→I，速率 σ）
3. 隔离（I→Q，随后 E→Q；受日容量约束）
4. 恢复（I→R 速率 γ；Q→R 速率 γ_q）
5. 疫苗接种（S→R；受日容量约束）
6. 迁移（仅 metapop，本地流之后的一次性同步交换）

时间轴必须满足 `horizon_days` 是 `step_days` 的整数倍，否则以
`step_not_aligned` 拒绝。

## 2. 自相矛盾输入的拒绝（不静默套用默认值）

归一化（`model.Normalize`）只负责把旧版配置按**确定性规则**迁移；校验
（`Scenario.Validate`）负责拒绝矛盾输入。错误统一为带稳定 `Reason` 的
`ValidationError`，可被调用方区分：

| Reason | 触发条件 |
|---|---|
| `unsupported_version` / `malformed_json` | 未知配置版本或 JSON 无法解析 |
| `unknown_paradigm` / `unknown_model_type` | 不支持的口径/疾病类型 |
| `group_not_closed` | 群初始仓室之和 ≠ `size` |
| `duplicate_group_id` / `empty_groups` | 群 ID 重复或没有群 |
| `contact_matrix_shape` / `matrix_not_square` | 接触矩阵形状不符 |
| `migration_outflow_exceeds_one` | 迁移列（源）外流量/天>1，或 ×步长>1（单步会搬空斑块） |
| `migration_matrix_shape` / `missing_relation_matrix` | 矩阵缺失或形状错误 |
| `timeline_reversal` | 干预区间超出 `[0, horizon]` |
| `bad_time_order` | 干预 start ≥ end |
| `intervention_interval_overlap` | **同类**干预区间重叠（不同类允许重叠） |
| `bad_compliance` / `bad_strength` / `bad_rate` / `bad_capacity` | 参数越界（依从性/强度须 ∈[0,1]，容量与速率须 ≥0） |
| `group_reference_out_of_range` | 干预引用了不存在的群 |
| `step_not_aligned` / `non_positive_step` / `bad_horizon` | 时间轴设置非法 |
| `non_finite_value` / `negative_value` | 数值非有限或为负 |

### 旧版 v1 → v2 归一化规则（确定性）

`"version":"1"` 的旧配置按下表迁移后继续可用（不猜测、不补默认矛盾值）：

`days→horizon_days`；`step_days=1`；`seed=1`；单群 `all`（`init_s=pop_size-init_i`）；
接触矩阵 `[[1]]`；`beta = r0 × recovery`；`gamma=gamma_q=recovery`；口径
`grouped`、类型 `sir`；封城字段映射为一条 `contact_reduction`（依从性 1）。

## 3. 干预与容量规则

干预在**半开区间**生效：对从 t 推进到 t+dt 的第 k 步，当且仅当
`start_day ≤ t < end_day` 时该干预活跃。因此生效与失效时点是确定的网格无关
时刻，不依赖步长碰巧取整。

- **接触减少** `contact_reduction`：目标群的接触矩阵行乘以
  `(1 − compliance × strength)`；strength、compliance 均 ∈ [0,1]。
- **疫苗接种** `vaccination`：按 `rate × compliance` 的人均速率把 S 移到 R；
  每个群每天最多移动 `capacity` 人（`capacity ≤ 0` 表示不限量）。
- **隔离** `isolation`：按 `rate × compliance` 把 I（优先）与 E 移到 Q；
  同样受每日每群容量约束。

容量触顶不是错误：被钳制后的实际人数被记录为 `CapacityEvent`，精确定位
**日期、步号、群、干预类型、请求量、实际量、容量与文字原因**，推演继续且
状态保持物理（无负数/非有限值；每群总量守恒）。若任何更新后检测到非物理
状态，则返回带精确时间点与原因的 `CapacityError`（这是校验之外的安全网，
合法输入不应触发）。

## 4. 参数估计：收敛判定与不确定性

仅估计一维参数 β（目标是让收敛标准完全可解释）。

- **目标函数**：观测发病数相对确定性模型发病数的泊松负对数似然（NLL），
  模型均值取 1e-9 下限以保持有限。
- **搜索**：可行域 `[BetaLow, BetaHigh]` 上的黄金分割搜索，带确定性求值缓存。
- **收敛（converged）**：在 `MaxIterations` 内括号宽度 ≤ `Tolerance`，且最优点
  严格位于可行域内部。
- **未收敛如实报告**，不输出看似有效的结果：
  - `not_converged_max_iterations`：迭代耗尽括号仍宽于容差；
  - `not_converged_flat_likelihood`：连续 3 次迭代似然改善小于容差（平台）；
  - `out_of_feasible_domain`：最优点贴在括号边界（需扩大搜索范围），或 95%
    区间无法在可行域内闭合。
- **口径不兼容** `incompatible_observations`：观测序列的 paradigm/model_type
  与情景不一致、观测日不在步长网格上或超出时间轴、群引用越界。

### 95% 不确定性区间的含义

采用**剖面似然（profile likelihood）区间**：分别从 β̂ 向两侧二分搜索使
NLL 上升到 `NLL(β̂) + 1.9207`（即 ½·χ²₀.₉₅,df=1 = ½×3.8415）的 β 值。
它表示：在给定泊松观测模型下，似然比不超过该阈值的参数集合。若搜索到可行
域边缘仍未达到阈值，区间被标记为 `ci_clipped` 且整体状态置为
`out_of_feasible_domain`，不会伪造一个有限的漂亮区间。

### 检查点续算与资源有界

- 搜索过程每完成一次迭代，把**四元黄金分割状态 + 扁平求值缓存**原子写入
  单个检查点文件（临时文件 + rename）。检查点只含优化器状态（O(迭代数)，
  对固定容差有界），**不含人口向量**，故人口或时间轴放大不影响检查点体积。
- 续算要求检查点括号与请求括号完全一致，否则明确拒绝（避免混用两个问题）。
- 续算后的试值序列、β̂、迭代数、95% 区间与一次不间断运行**完全一致**；
  收敛完成后检查点自动清除。

引擎级别的快照续算（`engine.RunFrom` / `checkpoint.Store`）在任意步的
`State` 上恢复；恢复轨迹与不间断运行逐位一致（有单测断言）。`RunWithOptions`
可对长程推演限制保留的状态样本数（终点必保留；发病数台账与容量事件不丢弃），
使内存占用有界。

## 5. 确定性与并行

积分过程本身是确定性的；合成观测的泊松噪声由情景 `seed` 播种、每次生成
本地新建 RNG 并按步顺序消费。`engine.RunScenarios` 各工作副本互不共享可变
状态，结果按输入索引返回——worker 数（1/2/4/…）不会改变任何结论。

## 6. 本地验证方法

```bash
go vet ./...
go test ./... -v        # 含：非法配置分类、容量受限、非物理守卫、
                        # 估计不收敛/越界/不兼容、检查点续算、并行确定性
go test ./... -race     # 竞态检测
go run ./cmd/episim demo
go run ./cmd/episim resume-demo
go run ./cmd/episim determinism -f <your_scenario.json>
```

单测日志打印关键输入（完整情景摘要）、随机种子、容量事件的时间/原因以及
收敛/拒绝判定依据。情景 JSON 示例见 `testdata/`。

## 包结构

```
model/       口径、状态、矩阵校验、情景归一化/校验、确定性动力学、干预定义
engine/      确定性推演主循环、并行执行、有界轨迹、续算入口
synthetic/   本地种子化合成观测（泊松测量噪声）
estimate/    黄金分割估计、收敛判定、剖面似然区间、检查点续算
checkpoint/  单快照原子存储、通用原子 JSON 读写
cmd/episim/  命令行入口与内置演示
internal/testutil/ 测试情景夹具
```
