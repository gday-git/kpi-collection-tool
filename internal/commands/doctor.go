package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/config"
	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/database"
	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/task"

	"github.com/spf13/cobra"
)

type checkStatus int

const (
	statusPass checkStatus = iota
	statusWarn
	statusFail
)

type check struct {
	status  checkStatus
	message string
}

func (c check) String() string {
	tag := "[UNKNOWN]"
	switch c.status {
	case statusPass:
		tag = "[PASS]"
	case statusWarn:
		tag = "[WARN]"
	case statusFail:
		tag = "[FAIL]"
	}
	return fmt.Sprintf("  %s %s", tag, c.message)
}

// doctorFlags holds flags specific to the doctor command.
// We use a separate InputFlags instance so that doctor does not share
// mutable state with run.
var doctorFlags config.InputFlags

var doctorCmd = &cobra.Command{
	Use:          "doctor",
	Short:        "Validate configuration without running a collection",
	SilenceUsage: true,
	Long: `Lint and validate the KPI collector configuration (CLI flags,
KPI/tasks YAML, environment variables, file paths) and report what is
set correctly and what needs attention.

No data is collected and no connections are made.`,
	Example: `  # Lint a KPI YAML file only
  kpi-collector doctor --prom-kpis-config kpis.yaml

  # Lint a tasks file covering all task types
  kpi-collector doctor --tasks tasks.yaml

  # Full pre-flight check (config + cluster + auth + database)
  kpi-collector doctor --cluster-name prod --cluster-type ran \
    --kubeconfig ~/.kube/config --prom-kpis-config kpis.yaml

  # Full pre-flight check with PostgreSQL
  kpi-collector doctor --cluster-name prod --cluster-type ran \
    --token $TOKEN --thanos-url thanos.example.com \
    --tasks tasks.yaml --db-type postgres --postgres-url "postgresql://user:pass@host:5432/kpi"`,
	RunE: runDoctor,
}

func init() {
	rootCmd.AddCommand(doctorCmd)

	doctorCmd.Flags().StringVar(&doctorFlags.BearerToken, "token", "",
		"bearer token for Thanos authentication")
	doctorCmd.Flags().StringVar(&doctorFlags.ThanosURL, "thanos-url", "",
		"Thanos querier URL (without https://)")
	doctorCmd.Flags().StringVar(&doctorFlags.Kubeconfig, "kubeconfig", "",
		"path to kubeconfig file for auto-discovery")
	doctorCmd.Flags().StringVar(&doctorFlags.ClusterName, "cluster-name", "",
		"cluster name")
	doctorCmd.Flags().StringVar(&doctorFlags.ClusterType, "cluster-type", "",
		"cluster type: ran, core, or hub")
	doctorCmd.Flags().BoolVar(&doctorFlags.InsecureTLS, "insecure-tls", false,
		"skip TLS certificate verification (development only)")
	doctorCmd.Flags().DurationVar(&doctorFlags.SamplingFreq, "frequency", 60*time.Second,
		"sampling frequency (e.g. 30s, 1m, 2h)")
	doctorCmd.Flags().DurationVar(&doctorFlags.Duration, "duration", 45*time.Minute,
		"total duration for sampling (e.g. 10s, 1m, 2h)")
	doctorCmd.Flags().StringVar(&doctorFlags.DatabaseType, "db-type", "sqlite",
		"database type: sqlite (default) or postgres")
	doctorCmd.Flags().StringVar(&doctorFlags.PostgresURL, "postgres-url", "",
		"PostgreSQL connection string (required if db-type=postgres)")
	doctorCmd.Flags().StringVar(&doctorFlags.TasksConfig, "tasks", "",
		"path to tasks YAML file, or a directory containing tasks.yaml")
	doctorCmd.Flags().StringVar(&doctorFlags.PromKPIsConfig, "prom-kpis-config", "",
		"path to Prometheus KPI configuration file")
	doctorCmd.Flags().BoolVar(&doctorFlags.SingleRun, "once", false,
		"collect all KPI metrics once and exit (ignores --frequency and --duration)")
}

func runDoctor(_ *cobra.Command, _ []string) error {
	var checks []check

	checks = appendFlagChecks(checks)
	checks = appendFileChecks(checks)
	checks = appendConfigChecks(checks)
	checks = appendDatabaseChecks(checks)
	checks = appendArtifactsCheck(checks)

	return printReport(checks)
}

// appendFlagChecks validates CLI flags and appends results.
func appendFlagChecks(checks []check) []check {
	checks = appendClusterChecks(checks)
	checks = appendAuthChecks(checks)
	checks = appendTaskConfigFlagChecks(checks)
	checks = appendSamplingChecks(checks)
	return checks
}

func appendClusterChecks(checks []check) []check {
	if doctorFlags.ClusterName == "" && doctorFlags.ClusterType == "" {
		return checks
	}

	if doctorFlags.ClusterName != "" {
		checks = append(checks, check{statusPass, fmt.Sprintf("Cluster name: %s", doctorFlags.ClusterName)})
	} else {
		checks = append(checks, check{statusWarn, "Cluster name is not set (--cluster-name); required for run"})
	}

	if err := config.ValidateClusterType(doctorFlags.ClusterType); err != nil {
		if doctorFlags.ClusterType == "" {
			checks = append(checks, check{statusWarn, "Cluster type is not set (--cluster-type); required for run"})
		} else {
			checks = append(checks, check{statusFail,
				fmt.Sprintf("Invalid cluster type %q (must be %s)",
					doctorFlags.ClusterType, strings.Join(config.ValidClusterTypes, ", "))})
		}
	} else {
		checks = append(checks, check{statusPass, fmt.Sprintf("Cluster type: %s", doctorFlags.ClusterType)})
	}
	return checks
}

func appendAuthChecks(checks []check) []check {
	hasToken := doctorFlags.BearerToken != "" && doctorFlags.ThanosURL != ""
	hasKube := doctorFlags.Kubeconfig != ""
	hasAny := doctorFlags.BearerToken != "" || doctorFlags.ThanosURL != "" || hasKube

	if !hasAny {
		return checks
	}

	if err := config.ValidateAuth(doctorFlags.BearerToken, doctorFlags.ThanosURL, doctorFlags.Kubeconfig); err != nil {
		switch {
		case hasToken && hasKube:
			checks = append(checks, check{statusFail,
				"Authentication: both token/thanos-url and kubeconfig provided (pick one)"})
		case doctorFlags.BearerToken != "":
			checks = append(checks, check{statusFail, "Authentication: --token provided without --thanos-url"})
		case doctorFlags.ThanosURL != "":
			checks = append(checks, check{statusFail, "Authentication: --thanos-url provided without --token"})
		default:
			checks = append(checks, check{statusFail, err.Error()})
		}
	} else if hasToken {
		checks = append(checks, check{statusPass, "Authentication: token + thanos-url"})
	} else {
		checks = append(checks, check{statusPass,
			fmt.Sprintf("Authentication: kubeconfig (%s)", doctorFlags.Kubeconfig)})
	}

	if doctorFlags.InsecureTLS {
		checks = append(checks, check{statusWarn, "TLS certificate verification is disabled (--insecure-tls)"})
	}
	return checks
}

func appendTaskConfigFlagChecks(checks []check) []check {
	if err := config.ValidateTaskConfig(doctorFlags.TasksConfig, doctorFlags.PromKPIsConfig); err != nil {
		if !doctorFlags.HasAnyTaskConfig() {
			return append(checks, check{statusWarn, "No task config specified (use --prom-kpis-config or --tasks)"})
		}
		return append(checks, check{statusFail, err.Error()})
	}
	if doctorFlags.PromKPIsConfig != "" {
		checks = append(checks, check{statusPass,
			fmt.Sprintf("Task config: --prom-kpis-config %s", doctorFlags.PromKPIsConfig)})
	} else {
		checks = append(checks, check{statusPass,
			fmt.Sprintf("Task config: --tasks %s", doctorFlags.TasksConfig)})
	}
	return checks
}

func appendSamplingChecks(checks []check) []check {
	if doctorFlags.SingleRun {
		return append(checks, check{statusPass, "Sampling: single-run mode (--once)"})
	}
	if doctorFlags.SamplingFreq <= 0 {
		checks = append(checks, check{statusFail, "Sampling frequency must be > 0"})
	} else {
		checks = append(checks, check{statusPass, fmt.Sprintf("Sampling frequency: %s", doctorFlags.SamplingFreq)})
	}
	if doctorFlags.Duration <= 0 {
		checks = append(checks, check{statusFail, "Duration must be > 0"})
	} else {
		checks = append(checks, check{statusPass, fmt.Sprintf("Duration: %s", doctorFlags.Duration)})
	}
	return checks
}

// appendFileChecks verifies that referenced files exist on disk.
func appendFileChecks(checks []check) []check {
	if doctorFlags.Kubeconfig != "" {
		checks = appendFileExistsCheck(checks, "Kubeconfig", doctorFlags.Kubeconfig)
	}
	if doctorFlags.PromKPIsConfig != "" {
		checks = appendFileExistsCheck(checks, "KPI config", doctorFlags.PromKPIsConfig)
	}
	if doctorFlags.TasksConfig != "" {
		checks = appendFileOrDirCheck(checks, "Tasks config", doctorFlags.TasksConfig)
	}
	return checks
}

func appendFileExistsCheck(checks []check, label, path string) []check {
	if _, err := os.Stat(path); err != nil {
		return append(checks, check{statusFail, fmt.Sprintf("%s file not found: %s", label, path)})
	}
	return append(checks, check{statusPass, fmt.Sprintf("%s file exists: %s", label, path)})
}

func appendFileOrDirCheck(checks []check, label, path string) []check {
	info, err := os.Stat(path)
	if err != nil {
		return append(checks, check{statusFail, fmt.Sprintf("%s not found: %s", label, path)})
	}
	if info.IsDir() {
		tasksFile := filepath.Join(path, "tasks.yaml")
		if _, err := os.Stat(tasksFile); err != nil {
			return append(checks, check{statusFail,
				fmt.Sprintf("%s directory exists but tasks.yaml not found inside: %s", label, path)})
		}
	}
	return append(checks, check{statusPass, fmt.Sprintf("%s exists: %s", label, path)})
}

// appendConfigChecks loads and validates KPI/tasks YAML.
func appendConfigChecks(checks []check) []check {
	if doctorFlags.PromKPIsConfig != "" {
		return appendPromKPIChecks(checks)
	}
	if doctorFlags.TasksConfig != "" {
		return appendTasksSpecChecks(checks)
	}
	return checks
}

func appendPromKPIChecks(checks []check) []check {
	kpis, err := config.LoadKPIs(doctorFlags.PromKPIsConfig)
	if err != nil {
		return append(checks, check{statusFail, fmt.Sprintf("KPI YAML failed to load: %v", err)})
	}
	checks = append(checks, check{statusPass,
		fmt.Sprintf("KPI YAML parsed successfully (%d queries loaded)", len(kpis.Queries))})

	checks = appendKPIValidation(checks, kpis)
	return checks
}

func appendKPIValidation(checks []check, kpis config.KPIs) []check {
	validationErrors := config.ValidateKPIs(kpis)
	if len(validationErrors) > 0 {
		for _, e := range validationErrors {
			checks = append(checks, check{statusFail, fmt.Sprintf("KPI validation: %v", e)})
		}
	} else {
		checks = append(checks, check{statusPass,
			fmt.Sprintf("KPI validation passed (%d/%d valid)", len(kpis.Queries), len(kpis.Queries))})
	}

	checks = appendKPIWarnings(checks, kpis)
	return checks
}

func appendKPIWarnings(checks []check, kpis config.KPIs) []check {
	uncategorized := 0
	for _, q := range kpis.Queries {
		if q.Category == "" {
			uncategorized++
		}
	}
	if uncategorized >= uncategorizedThreshold {
		checks = append(checks, check{statusWarn,
			fmt.Sprintf("%d KPIs have no category set (performance may degrade at scale)", uncategorized)})
	}

	if config.RequiresCPUSubstitution(kpis) && doctorFlags.Kubeconfig == "" {
		checks = append(checks, check{statusFail,
			"Queries contain CPU placeholders but no --kubeconfig provided"})
	}

	if !doctorFlags.SingleRun {
		checks = appendFrequencyWarnings(checks, kpis)
	}
	return checks
}

func appendFrequencyWarnings(checks []check, kpis config.KPIs) []check {
	for _, kpi := range kpis.Queries {
		if kpi.IsRunOnce() {
			continue
		}
		freq := kpi.GetEffectiveFrequency(doctorFlags.SamplingFreq)
		if freq > doctorFlags.Duration {
			checks = append(checks, check{statusWarn,
				fmt.Sprintf("KPI %q: frequency %s exceeds duration %s (only 1 sample)", kpi.ID, freq, doctorFlags.Duration)})
		}
	}

	if err := config.ValidateRangeFrequency(kpis, doctorFlags.SamplingFreq); err != nil {
		checks = append(checks, check{statusFail, err.Error()})
	}
	return checks
}

func appendTasksSpecChecks(checks []check) []check {
	spec, err := config.LoadTasksSpec(doctorFlags.TasksConfig)
	if err != nil {
		return append(checks, check{statusFail, fmt.Sprintf("Tasks spec failed to load: %v", err)})
	}

	present := spec.PresentTaskConfigs()
	checks = append(checks, check{statusPass,
		fmt.Sprintf("Tasks spec loaded and validated (%d task type(s): %s)",
			len(present), strings.Join(present, ", "))})

	checks = appendTasksSpecOrchestration(checks, spec)
	checks = appendTasksSpecPrometheus(checks, spec)
	checks = appendTasksSpecPerNodeData(checks, spec)
	checks = appendTasksSpecOslat(checks, spec)
	checks = appendTasksSpecAppRecoveryTime(checks, spec)
	return checks
}

func appendTasksSpecOrchestration(checks []check, spec config.TasksSpec) []check {
	return append(checks, check{statusPass,
		fmt.Sprintf("Orchestration: mode=%s, on-failure=%s",
			spec.Orchestration.Mode, spec.Orchestration.OnFailure)})
}

func appendTasksSpecPrometheus(checks []check, spec config.TasksSpec) []check {
	if spec.Prometheus == nil {
		return checks
	}
	kpis, err := task.LoadPrometheusKPIs(spec)
	if err != nil {
		return append(checks, check{statusFail, fmt.Sprintf("Task [prometheus]: KPIs failed to load: %v", err)})
	}
	checks = append(checks, check{statusPass,
		fmt.Sprintf("Task [prometheus]: %d KPI(s) loaded", len(kpis.Queries))})
	checks = appendKPIValidation(checks, kpis)
	return checks
}

func appendTasksSpecPerNodeData(checks []check, spec config.TasksSpec) []check {
	if spec.PerNodeData == nil {
		return checks
	}
	checks = append(checks, check{statusPass,
		fmt.Sprintf("Task [per-node-data]: duration=%s, interval=%s, image=%s",
			spec.PerNodeData.Duration, spec.PerNodeData.Interval, spec.PerNodeData.Image)})
	if len(spec.PerNodeData.WorkloadNamespaces) > 0 {
		checks = append(checks, check{statusPass,
			fmt.Sprintf("Task [per-node-data]: workload namespaces: %s",
				strings.Join(spec.PerNodeData.WorkloadNamespaces, ", "))})
	}
	return checks
}

func appendTasksSpecOslat(checks []check, spec config.TasksSpec) []check {
	if spec.Oslat == nil {
		return checks
	}
	checks = append(checks, check{statusPass,
		fmt.Sprintf("Task [oslat]: timeout=%s", spec.Oslat.Timeout)})
	return checks
}

func appendTasksSpecAppRecoveryTime(checks []check, spec config.TasksSpec) []check {
	if spec.AppRecoveryTime == nil {
		return checks
	}
	checks = append(checks, check{statusPass,
		fmt.Sprintf("Task [app-recovery-time]: duration=%s, interval=%s",
			spec.AppRecoveryTime.Duration, spec.AppRecoveryTime.Interval)})
	return checks
}

// appendDatabaseChecks validates database configuration.
func appendDatabaseChecks(checks []check) []check {
	dbType := doctorFlags.DatabaseType

	envType := os.Getenv("KPI_COLLECTOR_DB_TYPE")
	envURL := os.Getenv("KPI_COLLECTOR_DB_URL")

	if envType != "" && dbType != envType {
		checks = append(checks, check{statusWarn,
			fmt.Sprintf("KPI_COLLECTOR_DB_TYPE=%q differs from --db-type=%q (flag takes precedence for run)", envType, dbType)})
	}

	pgURL := doctorFlags.PostgresURL
	if pgURL == "" {
		pgURL = envURL
	}

	if err := config.ValidateDatabaseType(dbType, pgURL); err != nil {
		return append(checks, check{statusFail, err.Error()})
	}

	switch dbType {
	case "sqlite":
		checks = append(checks, check{statusPass, "Database type: sqlite"})
		dbPath := filepath.Join(database.OutputDir, database.DefaultDBFileName)
		if _, err := os.Stat(dbPath); err != nil {
			checks = append(checks, check{statusWarn,
				fmt.Sprintf("SQLite database does not exist yet: %s (will be created on first run)", dbPath)})
		} else {
			checks = append(checks, check{statusPass, fmt.Sprintf("SQLite database found: %s", dbPath)})
		}
	case "postgres":
		checks = append(checks, check{statusPass, "Database type: postgres"})
		checks = append(checks, check{statusPass, "PostgreSQL URL is set"})
	}
	return checks
}

// appendArtifactsCheck validates the artifacts directory.
func appendArtifactsCheck(checks []check) []check {
	dir := database.OutputDir
	info, err := os.Stat(dir)
	if err != nil {
		checks = append(checks, check{statusWarn,
			fmt.Sprintf("Artifacts directory does not exist yet: %s (will be created on first run)", dir)})
	} else if !info.IsDir() {
		checks = append(checks, check{statusFail,
			fmt.Sprintf("Artifacts path exists but is not a directory: %s", dir)})
	} else {
		checks = append(checks, check{statusPass,
			fmt.Sprintf("Artifacts directory: %s", dir)})
	}
	return checks
}

func printReport(checks []check) error {
	fmt.Println("Configuration Report")
	fmt.Println("====================")
	var passed, warnings, errors int
	for _, c := range checks {
		fmt.Println(c)
		switch c.status {
		case statusPass:
			passed++
		case statusWarn:
			warnings++
		case statusFail:
			errors++
		}
	}
	fmt.Printf("\n%d passed, %d warning(s), %d error(s)\n", passed, warnings, errors)

	if errors > 0 {
		return fmt.Errorf("found %d configuration error(s)", errors)
	}
	return nil
}
