package engine

import (
	"sort"

	"github.com/lisama569/epidemic-simulation-go/internal/model"
)

// vaccineLedger 跟踪每个疫苗额度池在推演过程中的结转余额。
// 额度语义：额度池在 [StartDays,EndDays) 内每天提供 DailyLimit 剂，
// 步长 dt 对应的当步额度为 DailyLimit*dt（按步长比例折算）；
// Accumulate=false 时当步未用完的额度作废，true 时结转到后续步。
type vaccineLedger struct {
	quotas []model.VaccineQuota // 与 Scenario.VaccineQuotas 同序
	index  map[string]int       // 额度池 id -> 下标
	carry  []float64            // 每池结转余额（仅 Accumulate 池会累积）
}

func newVaccineLedger(sc *model.Scenario) *vaccineLedger {
	l := &vaccineLedger{
		quotas: sc.VaccineQuotas,
		index:  make(map[string]int, len(sc.VaccineQuotas)),
		carry:  make([]float64, len(sc.VaccineQuotas)),
	}
	for i, q := range sc.VaccineQuotas {
		l.index[q.ID] = i
	}
	return l
}

// vaxDemand 是一条接种需求单元（一项接种干预 × 一个目标分组）。
type vaxDemand struct {
	priority int
	quotaIdx int
	groupIdx int
	demand   float64
}

// allocate 计算本步各分组实际接种人数（S->V 的流量）。
//
// 配给规则（确定、稳定、可解释）：
//  1. 每个生效额度池本步可用额度 = DailyLimit*dt + 结转余额（仅 accumulate 池）；
//  2. 需求单元按 优先级降序 排序，优先级并列时保持情景声明顺序
//     （干预下标升序、组内分组顺序升序）——稳定决胜；
//  3. 按序贪心分配：每条需求分得 min(需求, 所属池剩余额度)，
//     任何时刻分配总量不超过当步额度，绝不出现负数；
//  4. 实际接种少于需求时记录 VaccinationEvent（时间、分组、额度池、
//     需求、实配、原因），未用完额度按 accumulate 规则结转或作废。
func (l *vaccineLedger) allocate(sc *model.Scenario, st *model.State, newInf []float64,
	active []model.Intervention, dt, t float64, events *[]model.VaccinationEvent) []float64 {
	g := len(sc.Groups)
	out := make([]float64, g)
	if len(l.quotas) == 0 {
		return out
	}

	// 1) 各池当步可用额度。
	avail := make([]float64, len(l.quotas))
	for i, q := range l.quotas {
		if t >= q.StartDays && t < q.EndDays {
			avail[i] = q.DailyLimit*dt + l.carry[i]
		} else {
			avail[i] = 0
			l.carry[i] = 0
		}
	}

	// 2) 汇总需求单元。需求以本步新感染后的剩余易感为上界，保证 S 不为负。
	var items []vaxDemand
	for _, iv := range active {
		if iv.Type != model.InterventionVaccination {
			continue
		}
		qi := l.index[iv.QuotaID]
		rate := iv.RatePerDay * iv.Compliance
		for _, gid := range iv.Groups {
			a := indexOf(sc, gid)
			sRem := st.S[a] - newInf[a]
			if sRem < 0 {
				sRem = 0
			}
			d := sRem * rate * dt
			if d > sRem {
				d = sRem
			}
			if d <= nonNegTol {
				continue
			}
			items = append(items, vaxDemand{
				priority: iv.Priority, quotaIdx: qi, groupIdx: a, demand: d,
			})
		}
	}

	// 3) 稳定配给：优先级降序；SliceStable 保证并列时维持声明顺序。
	sort.SliceStable(items, func(a, b int) bool {
		return items[a].priority > items[b].priority
	})
	for _, it := range items {
		alloc := it.demand
		if r := avail[it.quotaIdx]; alloc > r {
			alloc = r
		}
		if alloc < 0 {
			alloc = 0
		}
		avail[it.quotaIdx] -= alloc
		out[it.groupIdx] += alloc
		if it.demand > alloc+nonNegTol {
			*events = append(*events, model.VaccinationEvent{
				TimeDays:  t,
				GroupID:   sc.Groups[it.groupIdx].ID,
				QuotaID:   l.quotas[it.quotaIdx].ID,
				Demand:    it.demand,
				Allocated: alloc,
				Reason:    "vaccine_quota_exhausted",
			})
		}
	}

	// 4) 结转：仅 accumulate 池保留余额，其余作废。
	for i, q := range l.quotas {
		if q.Accumulate {
			l.carry[i] = avail[i]
		} else {
			l.carry[i] = 0
		}
	}
	return out
}
