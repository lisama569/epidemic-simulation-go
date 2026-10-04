package checkpoint

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/lisama569/epidemic-simulation-go/internal/model"
)

// simFileName 是推演断点在目录内的确定性文件名（与估计检查点分开存放）。
const simFileName = "simulation-checkpoint.json"

// SimState 是一次推演在某一推演步结束后的可序列化状态。
// 保存内容足以从断点原样续跑：
//   - Step：已完成的推演步数（下一步下标），对应日期 TimeDays = Step*stepDays，
//     可落在任意步上，不要求整日边界；
//   - 全部六个仓室（含免疫仓 V）的各分组人数；
//   - 每个额度池当时的结转余额 QuotaCarry（含 accumulate 池累积的余额）；
//   - 已记录的轨迹与容量/接种事件（续跑时直接续接，保证逐条一致）。
//
// 浮点数经 JSON 最短表示序列化后可精确往返，故续跑结果与不中断运行逐位一致。
type SimState struct {
	Step              int                      `json:"step"`
	TimeDays          float64                  `json:"timeDays"`
	S                 []float64                `json:"s"`
	E                 []float64                `json:"e"`
	I                 []float64                `json:"i"`
	R                 []float64                `json:"r"`
	Q                 []float64                `json:"q"`
	V                 []float64                `json:"v"`
	QuotaCarry        []float64                `json:"quotaCarry,omitempty"`
	Times             []float64                `json:"times"`
	CompS             [][]float64              `json:"compS"`
	CompE             [][]float64              `json:"compE"`
	CompI             [][]float64              `json:"compI"`
	CompR             [][]float64              `json:"compR"`
	CompQ             [][]float64              `json:"compQ"`
	CompV             [][]float64              `json:"compV"`
	CapacityEvents    []model.CapacityEvent    `json:"capacityEvents,omitempty"`
	VaccinationEvents []model.VaccinationEvent `json:"vaccinationEvents,omitempty"`
	ScenarioDigest    string                   `json:"scenarioDigest"`
}

// SaveSimulation 把推演断点写入 dir（单文件原子覆盖写，不累积）。
func SaveSimulation(dir string, st *SimState) error {
	if dir == "" {
		return model.NewError(model.ErrCheckpoint, "推演断点目录为空")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return model.NewError(model.ErrCheckpoint, "创建推演断点目录失败: %v", err)
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return model.NewError(model.ErrCheckpoint, "序列化推演断点失败: %v", err)
	}
	tmp := filepath.Join(dir, simFileName+".tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return model.NewError(model.ErrCheckpoint, "写推演断点临时文件失败: %v", err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, simFileName)); err != nil {
		return model.NewError(model.ErrCheckpoint, "替换推演断点文件失败: %v", err)
	}
	return nil
}

// LoadSimulation 从 dir 读取推演断点。
func LoadSimulation(dir string) (*SimState, error) {
	b, err := os.ReadFile(filepath.Join(dir, simFileName))
	if err != nil {
		return nil, model.NewError(model.ErrCheckpoint, "读取推演断点失败: %v", err)
	}
	var st SimState
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, model.NewError(model.ErrCheckpoint, "推演断点内容损坏: %v", err)
	}
	return &st, nil
}

// SimulationExists 报告推演断点是否存在。
func SimulationExists(dir string) bool {
	if dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, simFileName))
	return err == nil
}

// VerifyScenario 校验断点与当前情景摘要一致，不一致返回 ErrCheckpoint。
func (s *SimState) VerifyScenario(sc *model.Scenario) error {
	if Digest(sc) != s.ScenarioDigest {
		return model.NewError(model.ErrCheckpoint, "情景摘要与推演断点不一致，拒绝续跑")
	}
	return nil
}
