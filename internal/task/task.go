// Package task defines runnable units executed by `kpi-collector run`.
package task

import (
	"context"
	"fmt"
	"log"

	k8s "k8s.io/client-go/kubernetes"

	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/collector"
	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/config"
	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/kubernetes"
	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/output"
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

// removePod deletes a pod this task created. The pod name is printed to the
// terminal. A delete error is printed and logged, and does not hide the task result.
func removePod(ctx context.Context, client k8s.Interface, namespace, name, taskName string) {
	pod := namespace + "/" + name
	if err := kubernetes.DeletePod(ctx, client, namespace, name); err != nil {
		msg := fmt.Sprintf("failed to delete pod %s: %v", pod, err)
		output.PrintTaskProgress(taskName, msg)
		log.Printf("%s: %s", taskName, msg)
		return
	}
	output.PrintTaskProgress(taskName, "deleted pod "+pod)
}
