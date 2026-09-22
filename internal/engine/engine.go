// Package engine 实现确定性的分群体仓室模型推演（固定步长欧拉法）。
package engine

import (
	"sync"

	"github.com/lisama569/epidemic-simulation-go/internal/intervention"
	"github.com/lisama569/epidemic-simulation-go/internal/model"
)

// RunOptions 控制批量推演的并行资源。单情景推演本身无并行选项，
// 其结果由输入唯一决定。
type RunOptions struct {
	// Parallelism 为批量情景推演的工作协程数；结果集合与该值无关。
	// <=0 时取 1（顺序执行）。
	Parallelism int
}

// RunBatch 以指定并行度批量推演。每个情景独立构建时间轴，互不共享可变状态；
// 结果严格按输入下标顺序返回，因此输出与并行度无关、可逐位复现。
func RunBatch(scs []*model.Scenario, opts RunOptions) ([]*model.Trajectory, error) {
	p := opts.Parallelism
	if p <= 0 {
		p = 1
	}
	out := make([]*model.Trajectory, len(scs))
	errs := make([]error, len(scs))
	sem := make(chan struct{}, p)
	var wg sync.WaitGroup
	for i, sc := range scs {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, sc *model.Scenario) {
			defer wg.Done()
			defer func() { <-sem }()
			sched, err := intervention.Build(sc)
			if err != nil {
				errs[i] = err
				return
			}
			out[i], errs[i] = Run(sc, sched)
		}(i, sc)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
