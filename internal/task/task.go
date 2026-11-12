// Package task defines runnable units executed by `kpi-collector run`.
package task

import (
	"context"

	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/collector"
	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/config"
)

// Task is one unit of work `run` can execute.
type Task interface {
	Name() string
	Run(ctx context.Context) error
}

// PromKPITask collects Prometheus/Thanos KPI metrics.
type PromKPITask struct {
	kpis  config.KPIs
	flags config.InputFlags
}

// NewPromKPITask creates a Prometheus collection task.
func NewPromKPITask(kpis config.KPIs, flags config.InputFlags) *PromKPITask {
	return &PromKPITask{kpis: kpis, flags: flags}
}

func (t *PromKPITask) Name() string {
	return config.TaskConfigPrometheus
}

func (t *PromKPITask) Run(ctx context.Context) error {
	_ = ctx

	if t.flags.SingleRun {
		return collector.RunKPIsOnce(t.kpis, t.flags)
	}
	return collector.RunKPIs(t.kpis, t.flags)
}
