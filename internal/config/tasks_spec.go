package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Orchestration policy values for OrchestrationConfig.Mode and OrchestrationConfig.OnFailure.
const (
	ModeSequential = "sequential"
	ModeParallel   = "parallel"

	OnFailureContinue = "continue"
	OnFailureFailFast = "fail-fast"
)

// TaskConfig names used as YAML keys in a tasks.yaml file and, wherever
// task type identifiers are needed (e.g. OrchestrationConfig.Order, task.Task.Name()).
const (
	TaskConfigPrometheus      = "prometheus"
	TaskConfigOslat           = "oslat"
	TaskConfigPerNodeData     = "per-node-data"
	TaskConfigAppRecoveryTime = "app-recovery-time"
)

// knownTaskConfigs lists every task config key a tasks.yaml file may contain,
// in the order tasks run by default when orchestration.order is not set.
var knownTaskConfigs = []string{TaskConfigPrometheus, TaskConfigPerNodeData, TaskConfigOslat, TaskConfigAppRecoveryTime}

const tasksFileName = "tasks.yaml"

// OrchestrationConfig carries orchestration policy for a TasksSpec: how the
// present task configs are scheduled (sequential/parallel), how failures
// are handled, and an optional override of the default execution order.
type OrchestrationConfig struct {
	Mode      string   `yaml:"mode,omitempty"`
	OnFailure string   `yaml:"on-failure,omitempty"`
	Order     []string `yaml:"order,omitempty"`
}

// PrometheusTaskConfig configures the Prometheus/Thanos KPI task. Exactly
// one of ConfigFile or Kpis must be set — never both, never neither.
//
// The sampling fields below mirror their CLI-flag equivalents. When using
// --tasks they are the sole source; CLI --frequency/--duration/--once are
// rejected. Database settings (--db-type, --postgres-url) stay global CLI
// flags so other tasks can share the same store later.
type PrometheusTaskConfig struct {
	ConfigFile string    `yaml:"configFile,omitempty"`
	Kpis       []Query   `yaml:"kpis,omitempty"`
	Frequency  *Duration `yaml:"frequency,omitempty"`
	Duration   *Duration `yaml:"duration,omitempty"`
	Once       *bool     `yaml:"once,omitempty"`
}

// TasksSpec is the root of a --tasks YAML file: one optional task config per
// task type, plus orchestration policy in Orchestration.
type TasksSpec struct {
	Orchestration   OrchestrationConfig        `yaml:"orchestration,omitempty"`
	Prometheus      *PrometheusTaskConfig      `yaml:"prometheus,omitempty"`
	Oslat           *OslatTaskConfig           `yaml:"oslat,omitempty"`
	PerNodeData     *PerNodeDataTaskConfig     `yaml:"per-node-data,omitempty"`
	AppRecoveryTime *AppRecoveryTimeTaskConfig `yaml:"app-recovery-time,omitempty"`

	// baseDir is the directory containing the tasks file, used to resolve
	// relative configFile paths within task configs.
	baseDir string
}

// ResolvePath resolves a task config's configFile path relative to the
// directory containing the tasks file, leaving absolute paths unchanged.
func (c TasksSpec) ResolvePath(path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(c.baseDir, path)
}

// PresentTaskConfigs returns the task config keys that are set in this
// TasksSpec, in the fixed order defined by knownTaskConfigs.
func (c TasksSpec) PresentTaskConfigs() []string {
	var present []string
	if c.Prometheus != nil {
		present = append(present, TaskConfigPrometheus)
	}
	if c.PerNodeData != nil {
		present = append(present, TaskConfigPerNodeData)
	}
	if c.Oslat != nil {
		present = append(present, TaskConfigOslat)
	}
	if c.AppRecoveryTime != nil {
		present = append(present, TaskConfigAppRecoveryTime)
	}
	return present
}

// LoadTasksSpec reads a --tasks file and parses it into a TasksSpec.
// path may point directly at a YAML file, or at a directory containing a
// conventionally named "tasks.yaml".
func LoadTasksSpec(path string) (TasksSpec, error) {
	info, err := os.Stat(path)
	if err != nil {
		return TasksSpec{}, fmt.Errorf("cannot access tasks path %q: %w", path, err)
	}

	configPath := path
	if info.IsDir() {
		configPath = filepath.Join(path, tasksFileName)
	}

	data, err := os.ReadFile(configPath) //#nosec G304 -- path is user-provided CLI input
	if err != nil {
		return TasksSpec{}, fmt.Errorf("failed to open tasks file %q: %w", configPath, err)
	}

	var cfg TasksSpec
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return TasksSpec{}, fmt.Errorf("failed to decode tasks file %q: %w", configPath, err)
	}
	cfg.baseDir = filepath.Dir(configPath)

	if err := ValidateTasksSpec(&cfg); err != nil {
		return TasksSpec{}, err
	}

	return cfg, nil
}

// ValidateTasksSpec checks a parsed TasksSpec for structural correctness:
// at least one task config, valid orchestration policy, and (for the task
// configs whose schema is implemented today) valid content. It also fills
// in Orchestration's default mode/on-failure/order when left unset.
func ValidateTasksSpec(cfg *TasksSpec) error {
	present := cfg.PresentTaskConfigs()
	if len(present) == 0 {
		return fmt.Errorf("tasks file has no task configs defined (expected at least one of: %s)",
			strings.Join(knownTaskConfigs, ", "))
	}

	if err := validatePrometheusTaskConfig(cfg.Prometheus); err != nil {
		return err
	}
	if err := resolveAndValidateOslat(cfg); err != nil {
		return err
	}
	if err := resolveAndValidatePerNodeData(cfg); err != nil {
		return err
	}
	if err := resolveAndValidateAppRecoveryTime(cfg); err != nil {
		return err
	}

	return validateOrchestration(&cfg.Orchestration, present)
}

// validatePrometheusTaskConfig enforces that configFile and inline kpis are
// mutually exclusive, and that a present prometheus task config actually
// configures one of them (an empty `prometheus: {}` is almost certainly a
// mistake).
func validatePrometheusTaskConfig(p *PrometheusTaskConfig) error {
	if p == nil {
		return nil
	}

	hasConfigFile := p.ConfigFile != ""
	hasInlineKpis := len(p.Kpis) > 0

	if hasConfigFile && hasInlineKpis {
		return fmt.Errorf("prometheus: configFile and kpis are mutually exclusive — set only one")
	}
	if !hasConfigFile && !hasInlineKpis {
		return fmt.Errorf("prometheus: one of configFile or kpis must be set")
	}

	return nil
}

// validateOrchestration applies mode/on-failure/order defaults, validates
// their values, and (if the user set order) checks it against the task
// configs actually present. When order is omitted, it is filled with the
// default task-config order (present).
func validateOrchestration(orchestration *OrchestrationConfig, present []string) error {
	if orchestration.Mode == "" {
		orchestration.Mode = ModeSequential
	} else if orchestration.Mode != ModeSequential && orchestration.Mode != ModeParallel {
		return fmt.Errorf("orchestration.mode %q is invalid: must be %q or %q", orchestration.Mode, ModeSequential, ModeParallel)
	}

	if orchestration.OnFailure == "" {
		orchestration.OnFailure = OnFailureContinue
	} else if orchestration.OnFailure != OnFailureContinue && orchestration.OnFailure != OnFailureFailFast {
		return fmt.Errorf("orchestration.on-failure %q is invalid: must be %q or %q",
			orchestration.OnFailure, OnFailureContinue, OnFailureFailFast)
	}

	if len(orchestration.Order) == 0 {
		orchestration.Order = present
		return nil
	}

	return validateOrder(orchestration.Order, present)
}

// validateOrder requires orchestration.order to be exactly a permutation of
// the task configs present in the file: every entry must be a known task
// config key, no duplicates, and every present task config must be listed
// exactly once. This keeps the override unambiguous rather than mixing
// explicit and default ordering for a subset of task configs.
func validateOrder(order, present []string) error {
	presentSet := make(map[string]bool, len(present))
	for _, name := range present {
		presentSet[name] = true
	}

	seen := make(map[string]bool, len(order))
	for _, name := range order {
		if !slices.Contains(knownTaskConfigs, name) {
			return fmt.Errorf("orchestration.order: %q is not a known task config (expected one of: %s)",
				name, strings.Join(knownTaskConfigs, ", "))
		}
		if seen[name] {
			return fmt.Errorf("orchestration.order: %q is listed more than once", name)
		}
		seen[name] = true

		if !presentSet[name] {
			return fmt.Errorf("orchestration.order: %q is listed but has no corresponding task config in this file", name)
		}
	}

	var missing []string
	for _, name := range present {
		if !seen[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("orchestration.order is missing task config(s) present in this file: %s", strings.Join(missing, ", "))
	}

	return nil
}
