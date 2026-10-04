package validate

import (
	"fmt"
	"math"

	"github.com/lisama569/epidemic-simulation-go/internal/model"
)

// 校验所用的确定容差。
const (
	// gridTol 为时间点对齐与步数整除判定容差。
	gridTol = 1e-9
	// rowSumTol 为迁移矩阵行和等于 1 的判定容差（闭合约束）。
	rowSumTol = 1e-9
)

// Validate 对情景做全部自洽性校验。任一矛盾都返回带明确 ErrorKind 的错误，
// 绝不静默套用默认值。
func Scenario(sc *model.Scenario) error {
	if sc == nil {
		return model.NewError(model.ErrConfigSyntax, "情景为空")
	}
	if sc.ConfigVersion != 2 {
		return model.NewError(model.ErrUnsupportedVersion,
			"未归一的配置版本 configVersion=%d", sc.ConfigVersion)
	}
	switch sc.Paradigm {
	case model.ParadigmSIR, model.ParadigmSEIR:
	default:
		return model.NewError(model.ErrParameterOutOfRange,
			"未知建模口径 paradigm=%q（支持 sir/seir）", sc.Paradigm)
	}
	if err := validateParameters(sc); err != nil {
		return err
	}
	if err := validateTimeline(sc); err != nil {
		return err
	}
	g := len(sc.Groups)
	if g == 0 {
		return model.NewError(model.ErrPopulationNotClosed, "至少需要一个人口分组")
	}
	ids, err := validateGroups(sc)
	if err != nil {
		return err
	}
	if err := validateContact(sc, g); err != nil {
		return err
	}
	if err := validateMigration(sc, g); err != nil {
		return err
	}
	quotaIDs, err := validateVaccineQuotas(sc)
	if err != nil {
		return err
	}
	if err := validateInterventions(sc, ids, quotaIDs); err != nil {
		return err
	}
	return nil
}

// validateVaccineQuotas 校验疫苗额度池：id 非空唯一、每日额度非负有限
// （0 是合法的零额度，表示该池在生效期内不提供疫苗）、生效区间对齐网格
// 且不越界。任何矛盾都明确拒绝，不静默套默认值。
func validateVaccineQuotas(sc *model.Scenario) (map[string]bool, error) {
	ids := make(map[string]bool, len(sc.VaccineQuotas))
	for i, q := range sc.VaccineQuotas {
		at := func(format string, args ...any) error {
			return model.NewError(model.ErrVaccineQuotaInvalid,
				"vaccineQuotas[%d]: %s", i, fmt.Sprintf(format, args...))
		}
		if q.ID == "" {
			return nil, at("额度池缺少 id")
		}
		if ids[q.ID] {
			return nil, at("额度池 id %q 重复", q.ID)
		}
		ids[q.ID] = true
		if !finiteNonNeg(q.DailyLimit) {
			return nil, at("dailyLimit=%v 必须为非负有限值", q.DailyLimit)
		}
		if !(q.StartDays >= 0 && q.EndDays > q.StartDays && q.EndDays <= sc.HorizonDays+gridTol) {
			return nil, model.NewError(model.ErrVaccinationTimeline,
				"vaccineQuotas[%d]: 区间 [%.6f,%.6f) 不满足 0<=start<end<=horizon(%.6f)",
				i, q.StartDays, q.EndDays, sc.HorizonDays)
		}
		if !aligned(q.StartDays, sc.StepDays) || !aligned(q.EndDays, sc.StepDays) {
			return nil, model.NewError(model.ErrVaccinationTimeline,
				"vaccineQuotas[%d]: 区间端点必须落在步长 %.6f 的整数倍网格上", i, sc.StepDays)
		}
	}
	return ids, nil
}

func finiteNonNeg(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 }
func finitePos(v float64) bool    { return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0 }

func validateParameters(sc *model.Scenario) error {
	p := sc.Parameters
	if !finitePos(p.Beta) {
		return model.NewError(model.ErrParameterOutOfRange, "beta 必须为正有限值，实际=%v", p.Beta)
	}
	if !finitePos(p.Gamma) {
		return model.NewError(model.ErrParameterOutOfRange, "gamma 必须为正有限值，实际=%v", p.Gamma)
	}
	switch sc.Paradigm {
	case model.ParadigmSEIR:
		if !finitePos(p.Sigma) {
			return model.NewError(model.ErrParameterOutOfRange, "seir 口径下 sigma 必须为正有限值，实际=%v", p.Sigma)
		}
	case model.ParadigmSIR:
		if p.Sigma != 0 {
			return model.NewError(model.ErrParameterOutOfRange,
				"sir 口径下 sigma 必须为 0（不允许潜伏仓），实际=%v", p.Sigma)
		}
	}
	return nil
}

// validateTimeline 拒绝时间轴倒退、非正步长以及步长与区间不对齐的情形。
func validateTimeline(sc *model.Scenario) error {
	if !finitePos(sc.StepDays) {
		return model.NewError(model.ErrTimelineReversed, "stepDays 必须为正有限值，实际=%v", sc.StepDays)
	}
	if !(sc.HorizonDays > sc.StepDays) {
		return model.NewError(model.ErrTimelineReversed,
			"horizonDays(%v) 必须严格大于 stepDays(%v)", sc.HorizonDays, sc.StepDays)
	}
	ratio := sc.HorizonDays / sc.StepDays
	n := math.Round(ratio)
	if math.Abs(ratio-n) > gridTol {
		return model.NewError(model.ErrTimelineReversed,
			"horizonDays/stepDays=%v 不是整数步，推演时间轴无法对齐", ratio)
	}
	return nil
}

func validateGroups(sc *model.Scenario) (map[string]int, error) {
	ids := make(map[string]int, len(sc.Groups))
	for i, g := range sc.Groups {
		if g.ID == "" {
			return nil, model.NewError(model.ErrGroupReference, "groups[%d] 缺少 id", i)
		}
		if _, dup := ids[g.ID]; dup {
			return nil, model.NewError(model.ErrPopulationNotClosed, "分组 id 重复: %q", g.ID)
		}
		ids[g.ID] = i
		for name, v := range map[string]float64{"s0": g.S0, "e0": g.E0, "i0": g.I0, "r0": g.R0} {
			if !finiteNonNeg(v) {
				return nil, model.NewError(model.ErrPopulationNotClosed,
					"分组 %q 的 %s=%v 必须为非负有限值", g.ID, name, v)
			}
		}
		if g.S0+g.E0+g.I0+g.R0 <= 0 {
			return nil, model.NewError(model.ErrPopulationNotClosed,
				"分组 %q 初始总人口必须为正", g.ID)
		}
		if sc.Paradigm == model.ParadigmSIR && g.E0 != 0 {
			return nil, model.NewError(model.ErrPopulationNotClosed,
				"sir 口径下分组 %q 的 e0 必须为 0，实际=%v", g.ID, g.E0)
		}
	}
	return ids, nil
}

func validateContact(sc *model.Scenario, g int) error {
	m := sc.ContactMatrix
	if len(m) != g {
		return model.NewError(model.ErrContactInvalid,
			"接触矩阵行数 %d 与分组数 %d 不一致", len(m), g)
	}
	for i := range m {
		if len(m[i]) != g {
			return model.NewError(model.ErrContactInvalid,
				"接触矩阵第 %d 行长度 %d 不等于分组数 %d", i, len(m[i]), g)
		}
		for j, v := range m[i] {
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
				return model.NewError(model.ErrContactInvalid,
					"接触矩阵 C[%d][%d]=%v 必须为非负有限值", i, j, v)
			}
		}
	}
	return nil
}

// validateMigration 校验迁移矩阵：方阵、非负、有限且每行之和恰为 1（人口闭合）。
func validateMigration(sc *model.Scenario, g int) error {
	m := sc.MigrationMatrix
	if m == nil {
		return nil
	}
	if len(m) != g {
		return model.NewError(model.ErrMigrationInvalid,
			"迁移矩阵行数 %d 与分组数 %d 不一致", len(m), g)
	}
	for i := range m {
		if len(m[i]) != g {
			return model.NewError(model.ErrMigrationInvalid,
				"迁移矩阵第 %d 行长度 %d 不等于分组数 %d", i, len(m[i]), g)
		}
		sum := 0.0
		for j, v := range m[i] {
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
				return model.NewError(model.ErrMigrationInvalid,
					"迁移矩阵 M[%d][%d]=%v 必须为非负有限值", i, j, v)
			}
			sum += v
		}
		if math.Abs(sum-1.0) > rowSumTol {
			return model.NewError(model.ErrMigrationInvalid,
				"迁移矩阵第 %d 行之和为 %.12f，不等于 1（人口不闭合）", i, sum)
		}
	}
	return nil
}

func validateInterventions(sc *model.Scenario, ids map[string]int, quotaIDs map[string]bool) error {
	for i, iv := range sc.Interventions {
		if err := validateOneIntervention(sc, ids, quotaIDs, i, iv); err != nil {
			return err
		}
	}
	// 同类型、目标分组有交集且半开区间重叠 => 叠加口径不唯一，必须拒绝。
	// 接种安排之间的互相矛盾单独归类，与其他干预的区间重叠一眼可区分。
	for a := 0; a < len(sc.Interventions); a++ {
		for b := a + 1; b < len(sc.Interventions); b++ {
			x, y := sc.Interventions[a], sc.Interventions[b]
			if x.Type != y.Type {
				continue
			}
			if !intervalsOverlap(x.StartDays, x.EndDays, y.StartDays, y.EndDays) {
				continue
			}
			if !groupsIntersect(x.Groups, y.Groups, ids) {
				continue
			}
			if x.Type == model.InterventionVaccination {
				return model.NewError(model.ErrVaccinationScheduleConflict,
					"interventions[%d] 与 interventions[%d] 同为 vaccination 且目标分组在重叠区间相交（同一分组同一时段的接种安排互相矛盾）",
					a, b)
			}
			return model.NewError(model.ErrIntervalOverlap,
				"interventions[%d] 与 interventions[%d] 同为 %s 且目标分组在重叠区间相交",
				a, b, x.Type)
		}
	}
	return nil
}

func validateOneIntervention(sc *model.Scenario, ids map[string]int, quotaIDs map[string]bool, idx int, iv model.Intervention) error {
	at := func(format string, args ...any) error {
		return model.NewError(model.ErrInterventionInvalid,
			"interventions[%d]: %s", idx, fmt.Sprintf(format, args...))
	}
	atVax := func(format string, args ...any) error {
		return model.NewError(model.ErrVaccinationInvalid,
			"interventions[%d]: %s", idx, fmt.Sprintf(format, args...))
	}
	switch iv.Type {
	case model.InterventionContactReduction, model.InterventionIsolation, model.InterventionVaccination:
	default:
		return at("未知干预类型 %q", iv.Type)
	}
	if len(iv.Groups) == 0 {
		return at("目标分组列表为空")
	}
	seen := map[string]bool{}
	for _, gid := range iv.Groups {
		if _, ok := ids[gid]; !ok {
			return model.NewError(model.ErrGroupReference,
				"interventions[%d] 引用了不存在的分组 %q", idx, gid)
		}
		if seen[gid] {
			return at("目标分组 %q 重复出现", gid)
		}
		seen[gid] = true
	}
	// 时间轴问题按干预类型归类：接种相关单独归类，与其他干预一眼可区分。
	timelineKind := model.ErrTimelineReversed
	if iv.Type == model.InterventionVaccination {
		timelineKind = model.ErrVaccinationTimeline
	}
	if !(iv.StartDays >= 0 && iv.EndDays > iv.StartDays && iv.EndDays <= sc.HorizonDays+gridTol) {
		return model.NewError(timelineKind,
			"interventions[%d]: 区间 [%.6f,%.6f) 不满足 0<=start<end<=horizon(%.6f)",
			idx, iv.StartDays, iv.EndDays, sc.HorizonDays)
	}
	if !aligned(iv.StartDays, sc.StepDays) || !aligned(iv.EndDays, sc.StepDays) {
		return model.NewError(timelineKind,
			"interventions[%d]: 区间端点必须落在步长 %.6f 的整数倍网格上", idx, sc.StepDays)
	}
	if !(iv.Compliance > 0 && iv.Compliance <= 1) {
		return at("compliance=%v 必须落在 (0,1]", iv.Compliance)
	}
	switch iv.Type {
	case model.InterventionContactReduction:
		if !(iv.Strength > 0 && iv.Strength <= 1) {
			return at("contact_reduction 的 strength=%v 必须落在 (0,1]", iv.Strength)
		}
		if iv.RatePerDay != 0 || iv.Capacity != nil {
			return at("contact_reduction 不得设置 ratePerDay/capacity")
		}
		if iv.QuotaID != "" || iv.Priority != 0 {
			return at("contact_reduction 不得设置 quotaId/priority")
		}
	case model.InterventionIsolation:
		if !finitePos(iv.RatePerDay) {
			return at("isolation 的 ratePerDay=%v 必须为正有限值", iv.RatePerDay)
		}
		if iv.Strength != 0 {
			return at("isolation 不得设置 strength")
		}
		if iv.Capacity != nil && !finiteNonNeg(*iv.Capacity) {
			return at("capacity=%v 必须为非负有限值，或省略表示无界", *iv.Capacity)
		}
		if iv.QuotaID != "" || iv.Priority != 0 {
			return at("isolation 不得设置 quotaId/priority")
		}
	case model.InterventionVaccination:
		if !finitePos(iv.RatePerDay) {
			return atVax("vaccination 的 ratePerDay=%v 必须为正有限值", iv.RatePerDay)
		}
		if iv.Strength != 0 || iv.Capacity != nil {
			return atVax("vaccination 不得设置 strength/capacity")
		}
		if iv.QuotaID == "" {
			return model.NewError(model.ErrVaccineQuotaReference,
				"interventions[%d]: vaccination 必须声明 quotaId 引用一个疫苗额度池", idx)
		}
		if !quotaIDs[iv.QuotaID] {
			return model.NewError(model.ErrVaccineQuotaReference,
				"interventions[%d]: 引用了不存在的疫苗额度池 %q", idx, iv.QuotaID)
		}
		// 接种区间必须落在所引用额度池的生效区间内，否则存在无额度支撑的
		// 接种安排（会被静默零分配），属于口径矛盾，明确拒绝。
		for _, q := range sc.VaccineQuotas {
			if q.ID != iv.QuotaID {
				continue
			}
			if iv.StartDays < q.StartDays-gridTol || iv.EndDays > q.EndDays+gridTol {
				return atVax("接种区间 [%.6f,%.6f) 超出额度池 %q 的生效区间 [%.6f,%.6f)",
					iv.StartDays, iv.EndDays, q.ID, q.StartDays, q.EndDays)
			}
		}
	}
	return nil
}

// aligned 判断时间点 t 是否落在 step 的整数倍网格上。
func aligned(t, step float64) bool {
	r := math.Round(t / step)
	return math.Abs(t-r*step) <= gridTol
}

func intervalsOverlap(a, b, c, d float64) bool { return a < d-gridTol && c < b-gridTol }

func groupsIntersect(a, b []string, ids map[string]int) bool {
	set := map[string]bool{}
	for _, g := range a {
		set[g] = true
	}
	for _, g := range b {
		if set[g] {
			return true
		}
	}
	return false
}
