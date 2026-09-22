// Package checkpoint 提供参数估计过程的检查点落盘与恢复。
// 恢复时通过对情景、选项、观测的确定性摘要（规范 JSON 的 SHA-256）校验
// 一致性，摘要不匹配一律拒绝；恢复后续算的网格点位与不间断运行完全相同，
// 故轨迹逐位一致。检查点每轮仅一份固定大小文件，不随规模累积增长。
package checkpoint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/lisama569/epidemic-simulation-go/internal/model"
)

// fileName 是检查点在目录内的确定性文件名。
const fileName = "estimate-checkpoint.json"

// State 是一次估计在某一轮结束后的可序列化状态。
type State struct {
	Round          int             `json:"round"`
	GridLow        float64         `json:"gridLow"`
	GridHigh       float64         `json:"gridHigh"`
	GridStep       float64         `json:"gridStep"`
	BetaHat        float64         `json:"betaHat"`
	BestSSE        float64         `json:"bestSse"`
	LastShift      float64         `json:"lastShift"`
	ScenarioDigest string          `json:"scenarioDigest"`
	OptionsDigest  string          `json:"optionsDigest"`
	ObsDigest      string          `json:"obsDigest"`
	Scenario       *model.Scenario `json:"scenario,omitempty"`
}

// Digest 计算任意可 JSON 序列化对象的确定性摘要。
// json.Marshal 对 map 按键排序、对结构体按字段顺序输出，保证确定性。
func Digest(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		// 摘要对象全部来自本程序的确定结构，序列化失败属于程序缺陷。
		panic("checkpoint: digest marshal failure: " + err.Error())
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Save 写入检查点到 dir（确定性文件名，单文件覆盖写，不累积）。
func Save(dir string, st *State) error {
	if dir == "" {
		return model.NewError(model.ErrCheckpoint, "检查点目录为空")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return model.NewError(model.ErrCheckpoint, "创建检查点目录失败: %v", err)
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return model.NewError(model.ErrCheckpoint, "序列化检查点失败: %v", err)
	}
	tmp := filepath.Join(dir, fileName+".tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return model.NewError(model.ErrCheckpoint, "写检查点临时文件失败: %v", err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, fileName)); err != nil {
		return model.NewError(model.ErrCheckpoint, "替换检查点文件失败: %v", err)
	}
	return nil
}

// Load 从 dir 读取检查点。
func Load(dir string) (*State, error) {
	b, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		return nil, model.NewError(model.ErrCheckpoint, "读取检查点失败: %v", err)
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, model.NewError(model.ErrCheckpoint, "检查点内容损坏: %v", err)
	}
	return &st, nil
}

// Exists 报告检查点是否存在。
func Exists(dir string) bool {
	if dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, fileName))
	return err == nil
}

// Verify 校验检查点与当前输入摘要是否一致，不一致返回 ErrCheckpoint。
func (s *State) Verify(sc *model.Scenario, optsDigest string, obs model.ObservationSeries) error {
	if Digest(sc) != s.ScenarioDigest {
		return model.NewError(model.ErrCheckpoint, "情景摘要与检查点不一致，拒绝恢复")
	}
	if optsDigest != s.OptionsDigest {
		return model.NewError(model.ErrCheckpoint, "估计选项摘要与检查点不一致，拒绝恢复")
	}
	if Digest(obs) != s.ObsDigest {
		return model.NewError(model.ErrCheckpoint, "观测摘要与检查点不一致，拒绝恢复")
	}
	return nil
}
