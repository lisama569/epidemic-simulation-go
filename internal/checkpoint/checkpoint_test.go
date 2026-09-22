package checkpoint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lisama569/epidemic-simulation-go/internal/model"
)

func readDirNames(dir string) ([]string, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Readdirnames(-1)
}

// TestSingleFileBounded 验证多轮写入只覆盖单个固定大小文件，不随轮数累积。
func TestSingleFileBounded(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cp")
	sc := &model.Scenario{ConfigVersion: 2, Paradigm: model.ParadigmSIR}
	st := &State{Round: 1, GridLow: 0, GridHigh: 1, GridStep: 0.1,
		BetaHat: 0.5, ScenarioDigest: Digest(sc)}
	for r := 1; r <= 20; r++ {
		st.Round = r
		if err := Save(dir, st); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Round != 20 {
		t.Fatalf("检查点轮次=%d 期望 20", got.Round)
	}
	entries, _ := readDirNames(dir)
	if len(entries) != 1 {
		t.Fatalf("目录中文件数=%d，期望单文件覆盖写", len(entries))
	}
	t.Log("20 轮写入后检查点目录仍只有 1 个固定文件，资源占用不随轮数增长")
}

func TestDigestStableAndSensitive(t *testing.T) {
	a := &model.Parameters{Beta: 0.3, Gamma: 0.1}
	b := &model.Parameters{Beta: 0.3, Gamma: 0.1}
	c := &model.Parameters{Beta: 0.31, Gamma: 0.1}
	if Digest(a) != Digest(b) {
		t.Fatal("相同内容摘要应一致")
	}
	if Digest(a) == Digest(c) {
		t.Fatal("不同内容摘要不应碰撞")
	}
}
