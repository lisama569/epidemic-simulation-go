// 疫苗接种的运行时额度分配。
//
// 额度语义（对外可解释的投放计划）：
//   - 每个 VaccineQuota 是一个可被多个接种安排共享的“每日额度池”；
//   - 每个自然日 0 点补充 dosesPerDay 人份（按 stepDays 折算到每个积分步）；
//   - accumulate=false（默认）时，跨自然日未用完的额度清零；
//     accumulate=true 时滚入下一天；
//   - 同一自然日内的多个积分步共享当日额度，前一步用剩的部分后一步可用；
//   - 额度不足时，按 (priority 升序, 干预在情景中的下标升序, 分组下标升序)
//     的唯一全序依次满足，任何并列都有确定的决胜次序；
//   - 任何一步的总分配绝不超过池内剩余额度，所有人数非负。
package engine

import (
	"math"
	"sort"

	"github.com/lisama569/epidemic-simulation-go/internal/model"
)

// quotaRuntime 是一个额度池的跨步运行态。
type quotaRuntime struct {
	id         string
	perStep    float64 // 每积分步补充的额度 = dosesPerDay * stepDays
	accumulate bool
	remaining  float64 // 当前步可用的剩余额度
	currentDay int     // remaining 所属的自然日
}

// vaccineRuntime 汇总全部额度池。
type vaccineRuntime struct {
	pools map[string]*quotaRuntime
	// dayOrder 保证遍历额度池的次序确定（按声明顺序）。
	dayOrder []string
}

func newVaccineRuntime(sc *model.Scenario) *vaccineRuntime {
	vr := &vaccineRuntime{pools: map[string]*quotaRuntime{}}
	for i := range sc.VaccineQuotas {
		q := sc.VaccineQuotas[i]
		pr := &quotaRuntime{
			id: q.ID, perStep: q.DosesPerDay * sc.StepDays,
			accumulate: q.Accumulate, currentDay: -1,
		}
		vr.pools[q.ID] = pr
		vr.dayOrder = append(vr.dayOrder, q.ID)
	}
	return vr
}

// vaccRequest 是一个“接种安排 × 分组”在单步上的分配请求。
type vaccRequest struct {
	priority int
	ivIdx    int
	gIdx     int
	quotaID  string
	demand   float64 // 折算到本步、且已被当前 S 存量封顶的计划接种量
}

// apply 在一个积分步上执行全部生效的接种安排：先补充额度池，再按稳定
// 全序分配，最后把成功接种量写入 dV（由调用方从 S 扣除、计入 V），
// 并为每一处少接种追加可定位的 VaccinationEvent。
func (vr *vaccineRuntime) apply(sc *model.Scenario, st *model.State,
	active []model.Intervention, activeIdx []int, dt, t float64, dV []float64,
	events *[]model.VaccinationEvent) {

	if vr == nil || len(vr.pools) == 0 {
		return
	}
	day := dayOf(t)
	vr.refill(day)

	// 收集本步请求。active 与 activeIdx 同序且下标一一对应。
	reqs := make([]vaccRequest, 0)
	for n, iv := range active {
		if iv.Type != model.InterventionVaccination {
			continue
		}
		ivIdx := activeIdx[n]
		planned := iv.DosesPerDay * iv.Compliance * dt
		for _, gid := range iv.Groups {
			a := indexOf(sc, gid)
			req := vaccRequest{
				priority: iv.Priority, ivIdx: ivIdx, gIdx: a,
				quotaID: iv.QuotaID,
				demand:  math.Min(planned, st.S[a]),
			}
			reqs = append(reqs, req)
		}
	}
	if len(reqs) == 0 {
		return
	}

	// 稳定全序：priority 升序；并列时干预下标升序；再并列时分组下标升序。
	sort.SliceStable(reqs, func(i, j int) bool {
		if reqs[i].priority != reqs[j].priority {
			return reqs[i].priority < reqs[j].priority
		}
		if reqs[i].ivIdx != reqs[j].ivIdx {
			return reqs[i].ivIdx < reqs[j].ivIdx
		}
		return reqs[i].gIdx < reqs[j].gIdx
	})

	for _, r := range reqs {
		pool := vr.pools[r.quotaID]
		planned := sc.Interventions[r.ivIdx].DosesPerDay *
			sc.Interventions[r.ivIdx].Compliance * dt
		before := pool.remaining
		allocated := math.Min(r.demand, before)
		if allocated < 0 {
			allocated = 0
		}
		pool.remaining -= allocated
		dV[r.gIdx] += allocated

		// 记录少接种。判定依据明确：
		//   - 计划需求 > 本步可用额度（before），额度是瓶颈 => quota_exhausted；
		//   - 否则额度足以覆盖计划，但 S 存量不足导致实际接种少于计划
		//     => eligible_susceptible_exhausted。
		shortfall := planned - allocated
		if shortfall <= nonNegTol {
			continue
		}
		reason := model.VaccReasonEligibleExhausted
		if planned > before+nonNegTol {
			reason = model.VaccReasonQuotaExhausted
		}
		*events = append(*events, model.VaccinationEvent{
			TimeDays: t, Day: day, GroupID: sc.Groups[r.gIdx].ID,
			QuotaID: r.quotaID, Priority: r.priority, InterventionID: r.ivIdx,
			Demand: planned, Allocated: allocated, Shortfall: shortfall,
			Reason: reason,
		})
	}
}

// refill 把所有额度池推进到自然日 day 并补充当步额度。
func (vr *vaccineRuntime) refill(day int) {
	for _, id := range vr.dayOrder {
		p := vr.pools[id]
		if p.currentDay == day {
			// 同一自然日内：上一步结余直接可用，并补充本步额度。
			p.remaining += p.perStep
			continue
		}
		if p.currentDay >= 0 && !p.accumulate {
			p.remaining = 0 // 跨自然日且不累积：昨日结余作废
		}
		// accumulate=true 且跨日：remaining 原样保留后再补充。
		p.remaining += p.perStep
		p.currentDay = day
	}
}

// dayOf 返回时间 t 所属的自然日序号（步中点取整，消除浮点误差）。
func dayOf(t float64) int {
	return int(math.Floor(t + 1e-9))
}

// quotaSnapshot 是额度池运行态的可序列化快照。
type quotaSnapshot struct {
	ID         string  `json:"id"`
	Remaining  float64 `json:"remaining"`
	CurrentDay int     `json:"currentDay"`
}

// Snapshot 导出全部额度池的跨步状态（按声明顺序，保证序列化确定）。
func (vr *vaccineRuntime) Snapshot() []quotaSnapshot {
	if vr == nil {
		return nil
	}
	out := make([]quotaSnapshot, 0, len(vr.dayOrder))
	for _, id := range vr.dayOrder {
		p := vr.pools[id]
		out = append(out, quotaSnapshot{ID: p.id, Remaining: p.remaining, CurrentDay: p.currentDay})
	}
	return out
}

// Restore 从快照恢复额度池跨步状态；快照必须与情景声明的额度池一一对应。
func (vr *vaccineRuntime) Restore(snaps []quotaSnapshot) {
	if vr == nil {
		return
	}
	byID := map[string]quotaSnapshot{}
	for _, s := range snaps {
		byID[s.ID] = s
	}
	for _, id := range vr.dayOrder {
		if s, ok := byID[id]; ok {
			vr.pools[id].remaining = s.Remaining
			vr.pools[id].currentDay = s.CurrentDay
		}
	}
}
