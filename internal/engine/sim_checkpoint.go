// 引擎推演的中途检查点：用于“接种进行到一半中断后续算”的场景。
// 检查点在某个积分步边界落盘：记录已完成步数、该时刻全部分组的仓室状态、
// 截至该时刻的事件（含接种额度分配明细）、额度池剩余/所在自然日，以及情景
// 摘要。恢复时摘要不一致一律拒绝；续算从同一状态、同一额度余量继续，
// 因而最终轨迹与事件流和不间断运行逐位一致。
package engine

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/lisama569/epidemic-simulation-go/internal/checkpoint"
	"github.com/lisama569/epidemic-simulation-go/internal/intervention"
	"github.com/lisama569/epidemic-simulation-go/internal/model"
)

// SimCheckpointFile 是引擎推演检查点的固定文件名。
const SimCheckpointFile = "sim-checkpoint.json"

// SimCheckpoint 是可序列化的推演中途状态。
type SimCheckpoint struct {
	ScenarioDigest string                   `json:"scenarioDigest"`
	StepCompleted  int                      `json:"stepCompleted"`
	State          model.State              `json:"state"`
	QuotaSnapshots []quotaSnapshot          `json:"quotaSnapshots,omitempty"`
	CapacityEvents []model.CapacityEvent    `json:"capacityEvents,omitempty"`
	VaccineEvents  []model.VaccinationEvent `json:"vaccineEvents,omitempty"`
}

// RunUntil 在完成 stopAfterSteps 个积分步后停止并返回检查点（不落盘）。
// stopAfterSteps 必须落在 [1, 总步数-1]（至少留一步给续算），否则返回
// 归类错误，不静默截断。
func RunUntil(sc *model.Scenario, sched *intervention.Schedule,
	stopAfterSteps int) (*SimCheckpoint, error) {
	n := totalSteps(sc)
	if stopAfterSteps < 1 || stopAfterSteps >= n {
		return nil, model.NewError(model.ErrCheckpoint,
			"中断步数 %d 必须落在 [1,%d]（总步数 %d，需在最后一步之前中断）",
			stopAfterSteps, n-1, n)
	}
	tr, st, snaps, err := runFrom(sc, sched, 0, nil, nil, stopAfterSteps)
	if err != nil {
		return nil, err
	}
	return &SimCheckpoint{
		ScenarioDigest: checkpoint.Digest(sc),
		StepCompleted:  stopAfterSteps,
		State:          cloneState(st),
		QuotaSnapshots: snaps,
		CapacityEvents: append([]model.CapacityEvent(nil), tr.CapacityEvents...),
		VaccineEvents:  append([]model.VaccinationEvent(nil), tr.VaccinationEvents...),
	}, nil
}

// ResumeAndRun 从检查点继续推演到 horizon，返回与不间断运行逐位一致的
// 完整轨迹。情景摘要不一致、检查点步数越界都明确拒绝。
func ResumeAndRun(sc *model.Scenario, sched *intervention.Schedule,
	cp *SimCheckpoint) (*model.Trajectory, error) {
	if cp == nil {
		return nil, model.NewError(model.ErrCheckpoint, "检查点为空，无法恢复")
	}
	if checkpoint.Digest(sc) != cp.ScenarioDigest {
		return nil, model.NewError(model.ErrCheckpoint,
			"情景摘要与检查点不一致，拒绝恢复（接种计划可能已被改动）")
	}
	n := totalSteps(sc)
	if cp.StepCompleted < 1 || cp.StepCompleted >= n {
		return nil, model.NewError(model.ErrCheckpoint,
			"检查点已完成步数 %d 越界，合法范围 [1,%d]", cp.StepCompleted, n-1)
	}
	st := cloneState(&cp.State)
	suffix, _, _, err := runFrom(sc, sched, cp.StepCompleted, &st, cp.QuotaSnapshots, 0)
	if err != nil {
		return nil, err
	}

	// 拼接检查点前缀与续算后缀。runFrom 恢复时记录的第一个点就是
	// t=stepCompleted，与前缀末点重复，拼接时去掉后缀首点。
	prefix, err := replayPrefix(sc, sched, cp)
	if err != nil {
		return nil, err
	}
	tr := prefix
	tr.Times = append(tr.Times, suffix.Times[1:]...)
	for g := range sc.Groups {
		tr.S[g] = append(tr.S[g], suffix.S[g][1:]...)
		tr.E[g] = append(tr.E[g], suffix.E[g][1:]...)
		tr.I[g] = append(tr.I[g], suffix.I[g][1:]...)
		tr.R[g] = append(tr.R[g], suffix.R[g][1:]...)
		tr.Q[g] = append(tr.Q[g], suffix.Q[g][1:]...)
		tr.V[g] = append(tr.V[g], suffix.V[g][1:]...)
	}
	// 事件流：前缀事件以检查点为准，后缀事件由 runFrom 已重新生成。
	tr.CapacityEvents = append(append([]model.CapacityEvent(nil),
		cp.CapacityEvents...), suffix.CapacityEvents...)
	tr.VaccinationEvents = append(append([]model.VaccinationEvent(nil),
		cp.VaccineEvents...), suffix.VaccinationEvents...)
	return tr, nil
}

// replayPrefix 重放到检查点步数，仅用于取得逐位一致的前缀轨迹数组；
// 其事件不采用（以检查点内事件为准），避免重复计算时产生任何分歧。
func replayPrefix(sc *model.Scenario, sched *intervention.Schedule,
	cp *SimCheckpoint) (*model.Trajectory, error) {
	tr, _, _, err := runFrom(sc, sched, 0, nil, nil, cp.StepCompleted)
	if err != nil {
		return nil, err
	}
	return tr, nil
}

func cloneState(st *model.State) model.State {
	cp := func(x []float64) []float64 { return append([]float64(nil), x...) }
	return model.State{S: cp(st.S), E: cp(st.E), I: cp(st.I), R: cp(st.R),
		Q: cp(st.Q), V: cp(st.V)}
}

// SaveSimCheckpoint 把检查点原子写入 dir/sim-checkpoint.json。
func SaveSimCheckpoint(dir string, cp *SimCheckpoint) error {
	if dir == "" {
		return model.NewError(model.ErrCheckpoint, "检查点目录为空")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return model.NewError(model.ErrCheckpoint, "创建检查点目录失败: %v", err)
	}
	b, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return model.NewError(model.ErrCheckpoint, "序列化推演检查点失败: %v", err)
	}
	tmp := filepath.Join(dir, SimCheckpointFile+".tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return model.NewError(model.ErrCheckpoint, "写检查点临时文件失败: %v", err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, SimCheckpointFile)); err != nil {
		return model.NewError(model.ErrCheckpoint, "替换检查点文件失败: %v", err)
	}
	return nil
}

// LoadSimCheckpoint 从目录读取推演检查点。
func LoadSimCheckpoint(dir string) (*SimCheckpoint, error) {
	b, err := os.ReadFile(filepath.Join(dir, SimCheckpointFile))
	if err != nil {
		return nil, model.NewError(model.ErrCheckpoint, "读取推演检查点失败: %v", err)
	}
	var cp SimCheckpoint
	if err := json.Unmarshal(b, &cp); err != nil {
		return nil, model.NewError(model.ErrCheckpoint, "推演检查点内容损坏: %v", err)
	}
	return &cp, nil
}
