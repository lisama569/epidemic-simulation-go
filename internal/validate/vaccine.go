package validate

import (
	"fmt"
	"math"

	"github.com/lisama569/epidemic-simulation-go/internal/model"
)

// validateVaccineQuotas 校验情景级疫苗额度池声明：
//   - id 非空且不重复；
//   - dosesPerDay 必须为非负有限值（为负、NaN、Inf 一律拒绝，不静默归零）；
//   - 仅当存在 vaccination 干预时允许声明额度池，孤立额度池视为配置笔误拒绝。
func validateVaccineQuotas(sc *model.Scenario) (map[string]model.VaccineQuota, error) {
	quotas := make(map[string]model.VaccineQuota, len(sc.VaccineQuotas))
	hasVacc := false
	for _, iv := range sc.Interventions {
		if iv.Type == model.InterventionVaccination {
			hasVacc = true
			break
		}
	}
	if !hasVacc && len(sc.VaccineQuotas) > 0 {
		return nil, model.NewError(model.ErrVaccineQuotaInvalid,
			"声明了 %d 个疫苗额度池，但情景中没有任何 vaccination 接种安排",
			len(sc.VaccineQuotas))
	}
	for i, q := range sc.VaccineQuotas {
		if q.ID == "" {
			return nil, model.NewError(model.ErrVaccineQuotaInvalid,
				"vaccineQuotas[%d] 缺少 id", i)
		}
		if _, dup := quotas[q.ID]; dup {
			return nil, model.NewError(model.ErrVaccineQuotaInvalid,
				"疫苗额度池 id 重复: %q", q.ID)
		}
		if math.IsNaN(q.DosesPerDay) || math.IsInf(q.DosesPerDay, 0) || q.DosesPerDay < 0 {
			return nil, model.NewError(model.ErrVaccineQuotaInvalid,
				"额度池 %q 的 dosesPerDay=%v 必须为非负有限值", q.ID, q.DosesPerDay)
		}
		quotas[q.ID] = q
	}
	return quotas, nil
}

// validateVaccinationIntervention 校验单条接种安排的字段与时间轴。
// 接种额度按“自然日”结算与补充，因此：
//   - 区间端点必须落在步长网格上（与其它干预一致）；
//   - 区间端点必须落在自然日边界（整数天）；
//   - 存在接种安排时，1 天必须能被 stepDays 整除（每个自然日含整数个积分步）。
func validateVaccinationIntervention(sc *model.Scenario, quotas map[string]model.VaccineQuota,
	idx int, iv model.Intervention) error {
	at := func(format string, args ...any) error {
		return model.NewError(model.ErrVaccinationInvalid,
			"interventions[%d]: %s", idx, fmt.Sprintf(format, args...))
	}
	if iv.DosesPerDay <= 0 || math.IsNaN(iv.DosesPerDay) || math.IsInf(iv.DosesPerDay, 0) {
		return at("vaccination 的 dosesPerDay=%v 必须为正有限值", iv.DosesPerDay)
	}
	if iv.QuotaID == "" {
		return at("vaccination 必须引用一个 quotaId")
	}
	if _, ok := quotas[iv.QuotaID]; !ok {
		return model.NewError(model.ErrVaccineQuotaInvalid,
			"interventions[%d]: 接种安排引用了不存在的额度池 quotaId=%q", idx, iv.QuotaID)
	}
	if iv.Strength != 0 || iv.RatePerDay != 0 || iv.Capacity != nil {
		return at("vaccination 不得设置 strength/ratePerDay/capacity")
	}
	if !dayAligned(iv.StartDays) || !dayAligned(iv.EndDays) {
		return model.NewError(model.ErrVaccinationTimeline,
			"interventions[%d]: 接种区间端点 %.6f,%.6f 必须落在自然日边界（整数天），"+
				"额度按自然日结算", idx, iv.StartDays, iv.EndDays)
	}
	stepsPerDay := 1.0 / sc.StepDays
	spd := math.Round(stepsPerDay)
	if math.Abs(stepsPerDay-spd) > gridTol {
		return model.NewError(model.ErrVaccinationTimeline,
			"存在接种安排时 1/stepDays=%.6g 必须为整数（每个自然日须含整数个积分步），实际 stepDays=%v",
			stepsPerDay, sc.StepDays)
	}
	return nil
}

// dayAligned 判断 t 是否为整数天（自然日边界）。
func dayAligned(t float64) bool {
	return math.Abs(t-math.Round(t)) <= gridTol
}
