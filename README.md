# 传染病建模与情景推演引擎（本地、确定性、可复现）

一套完全在本地运行的传染病建模与情景推演引擎，由本地合成数据与情景配置驱动，
**不依赖任何外部服务或真实数据**。目标是让不同建模口径、干预安排与并行执行
下的推演结论一致、可复现且可解释。

## 1. 包结构

| 包 | 职责 |
|---|---|
| `internal/model` | 核心领域类型、仓室/口径约定、归类错误类型 |
| `internal/config` | JSON 配置解析（v2 严格）、v1 旧配置按确定规则归一到 v2 |
| `internal/validate` | 情景全部自洽性校验，矛盾输入被明确拒绝（可区分错误种类） |
| `internal/intervention` | 分阶段干预时间轴（半开区间、确定性生效集合查询） |
| `internal/engine` | 确定性固定步长推演、容量约束、迁移、有界并行批量推演 |
| `internal/synthetic` | 固定种子的合成观测生成与内置参考情景 |
| `internal/estimate` | beta 参数网格估计、收敛判定、不确定性区间 |
| `internal/checkpoint` | 检查点摘要校验、单文件落盘与续算 |
| `cmd/epidemic` | `run` / `estimate` / `demo` 三个本地子命令 |

## 2. 建模口径与状态约定

### 2.1 仓室

每个分组在任一时刻持有五个仓室（人数，非负、有限）：

- `S` 易感；`E` 潜伏（感染但尚不具传播性）；`I` 感染且可传播；
- `R` 移除（康复/死亡，本模型不区分）；`Q` 被隔离（已从传播环移除）。

支持两种建模口径：

- `sir`：无 E 仓，`E0` 必须为 0、参数 `sigma` 必须为 0，否则拒绝；
- `seir`：`sigma`（E→I 每日速率）必须为正。

两种口径由同一推演框架表述，差异仅在状态机是否包含 E 仓。

### 2.2 群体结构与接触关系

- 分组数 `G≥1`，`id` 非空且唯一；每个分组初始总人口 `s0+e0+i0+r0>0`。
  任何仓室初值为负、非有限值都归类为 `population_not_closed`。
- 接触矩阵 `C` 为 `G×G`，`C[a][h]` 是分组 a 中每人每日与 h 的接触次数；
  必须为非负有限值。接触矩阵**不要求行和为 1**（它是接触计数，不是比例）。
- 迁移矩阵 `M` 为 `G×G`：`M[i][j]`（i≠j）是**每日**从 i 迁到 j 的人口比例
  （速率语义），`M[i][i]` 等价于不外迁比例；每行之和必须恰为 1
  （容差 1e-9），否则归类为 `migration_matrix_invalid`。

### 2.3 时间轴

- `t_k = k·stepDays`，`k=0..N`，要求 `horizonDays/stepDays` 为整数
  （容差 1e-9），否则归类 `timeline_reversed`，不静默取整。
- 感染力（作用于 [t_k, t_{k+1}) 一步）：

```
λ_a = beta · Σ_h C[a][h]·f_a · I_h / (S_h+E_h+I_h+R_h)
```

`f_a` 是接触缩减因子（见 §3）；分母只计活动人口（Q 在隔离中不传播）。

- 每步顺序：SEIR/SIR 转化（固定步长欧拉，单步流量裁剪到不超过存量）
  → 隔离准入（容量配给）→ 迁移重排。Q 不参与迁移，隔离者以相同 `gamma`
  进入 R。每一步对全部人口严格守恒，所有仓室非负。
- 迁移在任意 `stepDays` 下采用稳定子步进欧拉：子步长 h 满足
  `h·max_i Σ_{j≠i} M[i][j] ≤ 1`，单子步转移行和恒为 1，
  因此即使日外迁比例很高、dt>1 天，也不会出现负数或人口泄漏。

## 3. 干预与容量规则

### 3.1 类型与生效时点

- `contact_reduction`（接触缩减）：生效期内目标分组接触行乘以
  `f = 1 − strength·compliance`。`strength∈(0,1]`，`compliance∈(0,1]`。
- `isolation`（隔离）：以 `ratePerDay·compliance` 的有效速率把 I 仓个体
  移入 Q 仓；`ratePerDay>0`。

干预区间为**半开 `[startDays, endDays)`**：开始时点所在步立即生效，
结束时点所在步已失效。区间端点必须落在步长网格上，且
`0 ≤ start < end ≤ horizon`。

- 同一类型、目标分组有交集且区间重叠的两项干预被拒绝
  （`intervention_interval_overlap`，叠加口径不唯一）；
- 首尾相接（前一项 end == 后一项 start）不重叠，允许；
- 目标分组互不相交的重叠区间允许；不同类型允许同时生效。

### 3.2 容量约束

- 隔离干预可显式给出 `capacity`（同时在 Q 仓的人数上限，按干预目标分组
  生效）。`capacity` 省略 = 容量无界；**零值不是无界**（nil 与 0 语义不同，
  0 是合法的零容量）。
- 多分组容量按各声明上限的**最严格者**取有效上限。
- 每步准入 `admit = min(demand, I, available)`，`available = cap − Q`；
  需求超过可用容量时按可用量配给，绝不出现 Q 超容量或任何负仓室。
- 每次配给都产生一条 `CapacityEvent`，记录**时间点、分组、需求、可用量、
  实际准入量与原因**（`isolation_capacity_exhausted`），可直接定位。

## 4. 确定性与并行复现

- 推演本身**不使用随机数**：相同情景输出逐位相同。
- 随机数仅用于合成观测加噪：固定种子的独立 `rand.Rand`，
  按 (时间点, 分组) 确定顺序消费；同种子逐位复现，噪声截断为非负。
- 批量推演与参数评估支持有界工作协程；每个任务输入独立、结果按固定下标
  归并，因此**输出与并行度无关**（`go test -race` 下验证）。

## 5. 参数估计、收敛判定与不确定性

### 5.1 方法

对 `beta` 做确定性网格搜索：

1. 粗网格（奇数点、含可行域两端）全枚举；
2. 围绕当前最优点逐轮细化：步长减半、格点数不变，窗口必要时整体平移以
   **保持在可行域内**；
3. 并列时取较小 beta（确定的决胜规则）。

观测序列必须与情景同口径（paradigm 一致；sir 不得观测 E 仓；分组/仓室/
时间点必须存在且对齐网格；观测非负），否则归类
`observation_paradigm_mismatch`。

### 5.2 收敛判定（可解释）

- 记录每轮最优点相对本轮网格中心的位移 `|Δβ|`；
- **连续两轮** `|Δβ| < tolBeta` 判定收敛；
- 达到细化轮数上限仍不满足则 `converged=false`，并在 `reason` 中如实给出
  末轮位移与阈值，**不输出看似有效的结论**。

### 5.3 不确定性含义

采用 SSE 剖面的似然阈值区间：

```
{ β : SSE(β) ≤ SSE_min · (1 + 3.841/nObs) }
```

即以最优点为中心、大样本下近似 95%（卡方 df=1）的相对似然集合；
剖面点随结果返回（`profilePoints`）以便复算与解释，跨越阈值处线性插值。

- 若最优点贴在可行域端格点（剖面被截断），区间无意义：结果仍返回，
  但 `converged=false`，同时调用得到 `estimate_outside_feasible_domain`
  错误，原因明确写出可行域与估计值。

### 5.4 检查点续算与资源边界

- 每轮结束把（轮数、本轮网格、最优点、最佳 SSE）与情景/选项/观测的
  **SHA-256 摘要**写入 `<dir>/estimate-checkpoint.json`；
- 恢复时三者摘要必须全部匹配，否则归类 `checkpoint_invalid` 拒绝恢复；
- 下一轮网格由检查点状态按与不间断运行**完全相同**的规则推导，
  故续算轨迹逐位一致（见估计测试对 1..N 轮中断点的逐一验证）；
- 检查点始终是**单个固定大小文件**（原子覆盖写），不随轮数或情景规模
  累积增长。评估只保留当轮格点的 SSE 数组，内存随格点数有界。

## 6. 错误归类（可区分的失败原因）

| ErrorKind | 触发条件 |
|---|---|
| `config_syntax_invalid` | JSON 非法；v2 含未知字段（严格解析） |
| `unsupported_config_version` | 版本既不是 1 也不是 2 |
| `population_not_closed` | 初值负/非有限、总人口非正、id 重复、sir 带 E |
| `contact_matrix_invalid` | 接触矩阵形状错误或含负/非有限值 |
| `migration_matrix_invalid` | 迁移矩阵形状错误、负值或行和≠1 |
| `timeline_reversed` | 步长非正、区间不整除网格、区间倒退/越界/不对齐 |
| `intervention_interval_overlap` | 同类型同目标分组区间重叠 |
| `parameter_out_of_range` | beta/gamma/sigma 非法、口径与 sigma 矛盾、可行域非法等 |
| `intervention_invalid` | 干预类型/依从度/强度/速率/容量非法 |
| `group_reference_invalid` | 干预或观测引用不存在的分组 |
| `observation_paradigm_mismatch` | 观测与模型口径不兼容 |
| `estimate_not_converged` | 估计未达收敛（结果中如实报告） |
| `estimate_outside_feasible_domain` | 最优点贴可行域边界，剖面被截断 |
| `checkpoint_invalid` | 检查点缺失、损坏或摘要不匹配 |

任何矛盾输入都返回上述归类错误，**不静默套用默认值**。唯一的缺省归一发生在
v1→v2：旧干预缺少 `adherence` 时按确定规则取 1.0（旧格式中该字段语义统一），
并在文档中明示；其余数值字段缺失即拒绝。

## 7. 旧版本配置归一

`configVersion` 缺省视为 1。v1 → v2 的确定映射：

- `paradigm` 大小写不敏感映射到 `sir`/`seir`；
- `disease.transmission/recovery/incubation` → `parameters.beta/gamma/sigma`；
- `timeHorizon/timeStep` → `horizonDays/stepDays`；
- `population[].susceptible/exposed/infected/recovered` → `s0/e0/i0/r0`；
- `contacts` → `contactMatrix`，`migration` → `migrationMatrix`；
- `measures[].kind`：`lockdown`→`contact_reduction`
  （`efficacy`→`strength`），`quarantine`→`isolation`
  （`rate`→`ratePerDay`，`maxIsolated`→`capacity`）；
- `startTime/endTime` → `startDays/endDays`；`adherence` 缺省→1.0。

归一后仍走完整 v2 校验。参考文件：`testdata/scenario-v1-legacy.json`、
`testdata/scenario-v2.json`。

## 8. 本地验证方法

要求 Go 1.25+（在 1.26 下验证）。无需网络、无需外部数据。

```bash
# 全部测试（含竞态检测与重复运行）
go test -race -count=2 ./...

# 单个情景推演（v1 旧配置会被自动归一）
go run ./cmd/epidemic run -config testdata/scenario-v2.json
go run ./cmd/epidemic run -config testdata/scenario-v1-legacy.json

# 合成观测 + 参数估计（收敛判定/区间/并行）
go run ./cmd/epidemic estimate -config testdata/scenario-v2.json -parallelism 4

# 中断后续算（两轮输出的 betaHat 必须一致）
go run ./cmd/epidemic estimate -config testdata/scenario-v2.json \
  -checkpoint-dir ./cp -stop-after 3
go run ./cmd/epidemic estimate -config testdata/scenario-v2.json \
  -checkpoint-dir ./cp -parallelism 4

# 端到端演示：合成数据 → 推演 → 估计 → 并行一致性
go run ./cmd/epidemic demo
```

测试日志会打印关键输入、随机种子与判定依据，例如：

- 容量测试：`I0`、隔离速率、依从度、容量、首次容量事件时间/分组/需求；
- 合成数据：`seed`、`noiseSD`、同/异种子一致性结论；
- 估计：`betaHat`、区间、末轮位移、阈值、连续稳定轮数与收敛原因；
- 迁移：各 `stepDays` 下的守恒结论。
