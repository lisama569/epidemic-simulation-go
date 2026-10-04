package checkpoint

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/lisama569/epidemic-simulation-go/internal/model"
)

// runFileName 是推演级检查点在目录内的确定性文件名（与估计级检查点互不干扰）。
const runFileName = "run-checkpoint.json"

// RunState 是一次推演在某个步边界上的完整可恢复状态。
//
// 断点落在步 k 边界（时刻 t=k*stepDays，k 不必对应整天）：保存此刻
// 六个仓室（含 V 免疫仓）的全分组状态、每个疫苗额度池的结转余额
// （accumulate 池的累计结余，非结转池恒为 0）、已产生的容量/接种事件，
// 以及到该时刻为止的轨迹前缀。恢复时以上述状态原样续算，
// 后续轨迹与事件序列和不中断运行逐位一致。
type RunState struct {
	// Step 为已完成的步数；状态对应时刻 t=Step*stepDays。
	Step int `json:"step"`
	// 轨迹前缀（长度 Step+1，含 t=0）。
	GroupIDs []string    `json:"groupIds"`
	Times    []float64   `json:"times"`
	S        [][]float64 `json:"s"`
	E        [][]float64 `json:"e"`
	I        [][]float64 `json:"i"`
	R        [][]float64 `json:"r"`
	Q        [][]float64 `json:"q"`
	V        [][]float64 `json:"v"`
	// QuotaCarry 为各疫苗额度池在断点时刻的结转余额，
	// 与 Scenario.VaccineQuotas 同序。
	QuotaCarry []float64 `json:"quotaCarry"`
	// 断点前已产生的事件，恢复后继续向后追加。
	CapacityEvents    []model.CapacityEvent    `json:"capacityEvents,omitempty"`
	VaccinationEvents []model.VaccinationEvent `json:"vaccinationEvents,omitempty"`
	// ScenarioDigest 为情景的确定性摘要，不匹配一律拒绝恢复。
	ScenarioDigest string `json:"scenarioDigest"`
}

// Verify 校验检查点与当前情景摘要是否一致，不一致归类 checkpoint_invalid。
func (s *RunState) Verify(sc *model.Scenario) error {
	if Digest(sc) != s.ScenarioDigest {
		return model.NewError(model.ErrCheckpoint, "情景摘要与推演检查点不一致，拒绝恢复")
	}
	return nil
}

// SaveRun 原子覆盖写推演检查点（单文件，不随断点次数累积）。
func SaveRun(dir string, st *RunState) error {
	if dir == "" {
		return model.NewError(model.ErrCheckpoint, "检查点目录为空")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return model.NewError(model.ErrCheckpoint, "创建检查点目录失败: %v", err)
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return model.NewError(model.ErrCheckpoint, "序列化推演检查点失败: %v", err)
	}
	tmp := filepath.Join(dir, runFileName+".tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return model.NewError(model.ErrCheckpoint, "写推演检查点临时文件失败: %v", err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, runFileName)); err != nil {
		return model.NewError(model.ErrCheckpoint, "替换推演检查点文件失败: %v", err)
	}
	return nil
}

// LoadRun 从 dir 读取推演检查点。
func LoadRun(dir string) (*RunState, error) {
	b, err := os.ReadFile(filepath.Join(dir, runFileName))
	if err != nil {
		return nil, model.NewError(model.ErrCheckpoint, "读取推演检查点失败: %v", err)
	}
	var st RunState
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, model.NewError(model.ErrCheckpoint, "推演检查点内容损坏: %v", err)
	}
	return &st, nil
}

// RunExists 报告推演检查点是否存在。
func RunExists(dir string) bool {
	if dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, runFileName))
	return err == nil
}
