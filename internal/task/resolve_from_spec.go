package task

import (
	"context"
	"fmt"
	"time"

	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/config"
	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/database"
	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/kubernetes"
)

type taskBuilder func(config.TasksSpec, config.InputFlags) (Task, error)

var taskBuilders = map[string]taskBuilder{
	config.TaskConfigPrometheus:      buildPrometheusTask,
	config.TaskConfigOslat:           buildOslatTask,
	config.TaskConfigPerNodeData:     buildPerNodeDataTask,
	config.TaskConfigAppRecoveryTime: buildAppRecoveryTimeTask,
}

// ResolveFromTasksSpec builds the ordered list of tasks described by a TasksSpec.
// Unsupported task types return a "not yet supported" error instead of a Task.
func ResolveFromTasksSpec(spec config.TasksSpec, flags config.InputFlags) ([]Task, error) {
	if err := config.ValidateTasksSpec(&spec); err != nil {
		return nil, err
	}

	var tasks []Task
	for _, taskName := range spec.Orchestration.Order {
		build, ok := taskBuilders[taskName]
		if !ok {
			return nil, fmt.Errorf("%s: unknown task config", taskName)
		}
		t, err := build(spec, flags)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

func buildPrometheusTask(spec config.TasksSpec, flags config.InputFlags) (Task, error) {
	kpis, err := LoadPrometheusKPIs(spec)
	if err != nil {
		return nil, err
	}
	return NewPromKPITask(kpis, flags), nil
}

// LoadPrometheusKPIs reads prometheus queries from inline kpis or configFile.
func LoadPrometheusKPIs(spec config.TasksSpec) (config.KPIs, error) {
	if spec.Prometheus == nil {
		return config.KPIs{}, fmt.Errorf("%s: task config is missing", config.TaskConfigPrometheus)
	}

	if spec.Prometheus.ConfigFile != "" {
		kpis, err := config.LoadKPIs(spec.ResolvePath(spec.Prometheus.ConfigFile))
		if err != nil {
			return config.KPIs{}, fmt.Errorf("%s: %w", config.TaskConfigPrometheus, err)
		}
		return kpis, nil
	}

	return config.KPIs{Queries: spec.Prometheus.Kpis}, nil
}

// FromPromKPIsFlag builds the single prometheus task used by --prom-kpis-config.
func FromPromKPIsFlag(flags config.InputFlags, kpis config.KPIs) ([]Task, error) {
	if flags.PromKPIsConfig == "" {
		return nil, fmt.Errorf("no tasks configured: --prom-kpis-config is required")
	}
	return []Task{NewPromKPITask(kpis, flags)}, nil
}

func buildOslatTask(spec config.TasksSpec, flags config.InputFlags) (Task, error) {
	if flags.Kubeconfig == "" {
		return nil, fmt.Errorf("%s: --kubeconfig is required", config.TaskConfigOslat)
	}
	if spec.Oslat == nil {
		return nil, fmt.Errorf("%s: task config is missing", config.TaskConfigOslat)
	}
	t := NewOslatTask(*spec.Oslat, flags.Kubeconfig, database.OutputDir)
	t.cleanup = spec.Orchestration.Cleanup
	return t, nil
}

func buildPerNodeDataTask(spec config.TasksSpec, flags config.InputFlags) (Task, error) {
	if flags.Kubeconfig == "" {
		return nil, fmt.Errorf("%s: --kubeconfig is required", config.TaskConfigPerNodeData)
	}
	if spec.PerNodeData == nil {
		return nil, fmt.Errorf("%s: task config is missing", config.TaskConfigPerNodeData)
	}
	t := NewPerNodeDataTask(*spec.PerNodeData, flags.Kubeconfig, database.OutputDir)
	t.cleanup = spec.Orchestration.Cleanup
	return t, nil
}

func buildAppRecoveryTimeTask(spec config.TasksSpec, flags config.InputFlags) (Task, error) {
	if flags.Kubeconfig == "" {
		return nil, fmt.Errorf("%s: --kubeconfig is required", config.TaskConfigAppRecoveryTime)
	}
	if spec.AppRecoveryTime == nil {
		return nil, fmt.Errorf("%s: task config is missing", config.TaskConfigAppRecoveryTime)
	}
	cfg := *spec.AppRecoveryTime

	client, err := kubernetes.ClientsetFromKubeconfig(flags.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", config.TaskConfigAppRecoveryTime, err)
	}
	preflightCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := kubernetes.CheckAppRecoveryAccess(preflightCtx, client, cfg.NodeNames, cfg.WorkloadNamespaces, cfg.Image); err != nil {
		return nil, fmt.Errorf("%s: %w", config.TaskConfigAppRecoveryTime, err)
	}

	t := NewAppRecoveryTimeTask(cfg, flags.Kubeconfig, database.OutputDir)
	t.cleanup = spec.Orchestration.Cleanup
	return t, nil
}
