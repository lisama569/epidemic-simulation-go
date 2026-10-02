// Package intervention 实现分阶段干预在离散时间轴上的稳定生效逻辑。
//
// 生效时点约定（半开区间）：干预 [start,end) 在网格时间点 t_k 上生效，
// 当且仅当 start <= t_k < end。即开始时点所在步立即生效，结束时点所在步
// 已失效（措施作用于 [t_k, t_{k+1}) 这一步的速率）。
package intervention

import (
	"sort"

	"github.com/lisama569/epidemic-simulation-go/internal/model"
	"github.com/lisama569/epidemic-simulation-go/internal/validate"
)

// edge 是时间轴上的一个生效集合变更点。
type edge struct {
	time float64
	// delta 为 +1 表示该项在此时点生效，-1 表示失效。
	delta int
	ivIdx int
}

// Schedule 是预处理后的干预时间轴，供引擎在每个步长边界查询生效集合。
type Schedule struct {
	scenario *model.Scenario
	edges    []edge
}

// Build 对干预列表做完整校验（委托 validate.Scenario 统一口径）并构建时间轴。
func Build(sc *model.Scenario) (*Schedule, error) {
	if err := validate.Scenario(sc); err != nil {
		return nil, err
	}
	s := &Schedule{scenario: sc}
	for i := range sc.Interventions {
		iv := sc.Interventions[i]
		s.edges = append(s.edges,
			edge{time: iv.StartDays, delta: +1, ivIdx: i},
			edge{time: iv.EndDays, delta: -1, ivIdx: i},
		)
	}
	// 确定性排序：先按时间；同一时间点先处理失效(-1)再处理生效(+1)，
	// 使恰好首尾相接（end==start）的两项不会被同时视为生效。
	sort.SliceStable(s.edges, func(a, b int) bool {
		if s.edges[a].time != s.edges[b].time {
			return s.edges[a].time < s.edges[b].time
		}
		return s.edges[a].delta < s.edges[b].delta
	})
	return s, nil
}

// ActiveAt 返回给定绝对时间 t（天）正在生效的干预（半开区间 [start,end)）。
// 返回顺序按干预在情景中的原始下标排列，保证调用方遍历顺序确定。
func (s *Schedule) ActiveAt(t float64) []model.Intervention {
	idxs := s.ActiveIndicesAt(t)
	out := make([]model.Intervention, 0, len(idxs))
	for _, i := range idxs {
		out = append(out, s.scenario.Interventions[i])
	}
	return out
}

// ActiveIndicesAt 返回给定绝对时间 t（天）正在生效的干预在情景干预列表中的
// 原始下标，按下标升序排列，确定可复现。
func (s *Schedule) ActiveIndicesAt(t float64) []int {
	active := map[int]bool{}
	for _, e := range s.edges {
		if e.time > t {
			break
		}
		if e.delta == +1 {
			active[e.ivIdx] = true
		} else {
			// 半开区间：t == end 时不生效。edge 时间 <= t 即处理，
			// 而 end==t 时该失效边已被处理，故自然排除。
			delete(active, e.ivIdx)
		}
	}
	idxs := make([]int, 0, len(active))
	for i := range active {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)
	return idxs
}
