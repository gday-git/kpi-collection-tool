package commands

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/config"
	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/database"
	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/kubernetes"
	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/logger"
	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/output"
	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/task"

	"github.com/spf13/cobra"
)

// Use the existing InputFlags struct directly!
var flags config.InputFlags

// runCmd represents the collect command
var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run KPI collection tasks",
	Long: `Run KPI collection tasks against an OpenShift cluster. Supports four
task types: prometheus (Thanos metrics → database), per-node-data (node
diagnostics → artifact files), oslat (latency test → artifact file), and
app-recovery-time (reboot + pod status → artifact file).

Configure tasks in a tasks.yaml file and pass it via --tasks, or use
--prom-kpis-config for Prometheus-only collection. Supports two authentication
modes:
  1. Kubeconfig-based auto-discovery
  2. Manual bearer token and Thanos URL

All artifacts (database, logs, task output) are stored in
./kpi-collector-artifacts/ by default. Use --artifacts-dir to override.`,
	Example: `  # Multi-task run from a tasks file
  kpi-collector run --cluster-name prod --cluster-type ran \
    --kubeconfig ~/.kube/config --tasks task-profiles/tasks-quickstart.yaml

  # Prometheus-only via kubeconfig (auto-discovery of Thanos URL and token)
  kpi-collector run --cluster-name prod --cluster-type ran \
    --kubeconfig ~/.kube/config --prom-kpis-config kpis.yaml

  # Prometheus-only via manual credentials
  kpi-collector run --cluster-name prod --cluster-type core \
    --token $TOKEN --thanos-url thanos.example.com --prom-kpis-config kpis.yaml

  # Collect all KPIs once and exit
  kpi-collector run --cluster-name prod --cluster-type ran \
    --kubeconfig ~/.kube/config --prom-kpis-config kpis.yaml --once

  # Custom sampling: every 30s for 2 hours
  kpi-collector run --cluster-name prod --cluster-type ran \
    --kubeconfig ~/.kube/config --prom-kpis-config kpis.yaml \
    --frequency 30s --duration 2h

  # With PostgreSQL backend
  kpi-collector run --cluster-name prod --cluster-type hub \
    --kubeconfig ~/.kube/config --prom-kpis-config kpis.yaml \
    --db-type postgres --postgres-url "postgresql://user:pass@localhost:5432/kpi"`,
	RunE: runTasks,
}

func init() {
	rootCmd.AddCommand(runCmd)

	// Authentication flags
	runCmd.Flags().StringVar(&flags.BearerToken, "token", "",
		"bearer token for Thanos authentication")
	runCmd.Flags().StringVar(&flags.ThanosURL, "thanos-url", "",
		"Thanos querier URL (without https://)")
	runCmd.Flags().StringVar(&flags.Kubeconfig, "kubeconfig", "",
		"path to kubeconfig file for auto-discovery")
	runCmd.Flags().StringVar(&flags.ClusterName, "cluster-name", "",
		"cluster name (required)")
	runCmd.Flags().StringVar(&flags.ClusterType, "cluster-type", "",
		"cluster type for categorization: ran, core, or hub")
	runCmd.Flags().BoolVar(&flags.InsecureTLS, "insecure-tls", false,
		"skip TLS certificate verification (development only)")

	// Sampling flags
	runCmd.Flags().DurationVar(&flags.SamplingFreq, "frequency", 60*time.Second,
		"sampling frequency (e.g. 30s, 1m, 2h)")
	runCmd.Flags().DurationVar(&flags.Duration, "duration", 45*time.Minute,
		"total duration for sampling (e.g. 10s, 1m, 2h)")

	// Database flags
	runCmd.Flags().StringVar(&flags.DatabaseType, "db-type", "sqlite",
		"database type: sqlite (default) or postgres")
	runCmd.Flags().StringVar(&flags.PostgresURL, "postgres-url", "",
		"PostgreSQL connection string (required if db-type=postgres)")

	// Task config flags
	runCmd.Flags().StringVar(&flags.TasksConfig, "tasks", "",
		"path to tasks YAML file, or a directory containing tasks.yaml")
	runCmd.Flags().StringVar(&flags.PromKPIsConfig, "prom-kpis-config", "",
		"path to Prometheus KPI configuration file")
	runCmd.Flags().StringVar(&flags.PromKPIsConfig, "kpis-file", "",
		"[deprecated: use --prom-kpis-config] path to Prometheus KPI configuration file")

	if err := runCmd.Flags().MarkDeprecated("kpis-file", "use --prom-kpis-config instead"); err != nil {
		panic(fmt.Sprintf("failed to mark kpis-file as deprecated: %v", err))
	}

	// Single-run mode
	runCmd.Flags().BoolVar(&flags.SingleRun, "once", false,
		"collect all KPI metrics once and exit (ignores --frequency and --duration)")

	runCmd.Flags().BoolVar(&flags.Parallel, "parallel", false,
		"run tasks concurrently instead of sequentially (also settable via orchestration.mode in a tasks file)")

	// Skip interactive prompts
	runCmd.Flags().BoolVarP(&flags.SkipPrompts, "yes", "y", false,
		"skip interactive prompts (e.g. category advisory)")

	// Post-run archiving
	runCmd.Flags().BoolVar(&flags.TarArtifacts, "tar-artifacts", false,
		"create a .tar.gz archive of the artifacts directory after the run completes")

	// Mark required flags
	if err := runCmd.MarkFlagRequired("cluster-name"); err != nil {
		panic(fmt.Sprintf("failed to mark cluster-name as required: %v", err))
	}

	// --once is mutually exclusive with --frequency and --duration
	runCmd.MarkFlagsMutuallyExclusive("once", "frequency")
	runCmd.MarkFlagsMutuallyExclusive("once", "duration")
	runCmd.MarkFlagsMutuallyExclusive("tasks", "prom-kpis-config")
	runCmd.MarkFlagsMutuallyExclusive("tasks", "kpis-file")
	// With --tasks, sampling flags come from prometheus: in the YAML only.
	// --db-type and --postgres-url stay global and are allowed with --tasks.
	runCmd.MarkFlagsMutuallyExclusive("tasks", "frequency")
	runCmd.MarkFlagsMutuallyExclusive("tasks", "duration")
	runCmd.MarkFlagsMutuallyExclusive("tasks", "once")
}

// promOverrideFlags can override KPI YAML defaults when using --prom-kpis-config.
var promOverrideFlags = []string{"frequency", "duration", "db-type", "postgres-url", "once"}

func runTasks(cmd *cobra.Command, args []string) error {
	fmt.Println("KPI Collector starting...")

	if err := config.ValidateFlags(flags); err != nil {
		return fmt.Errorf("invalid flags: %w", err)
	}

	fmt.Printf("Cluster name: %s (type=%s)\n", flags.ClusterName, flags.ClusterType)

	if err := os.MkdirAll(database.OutputDir, 0755); err != nil {
		return fmt.Errorf("failed to create artifacts directory: %w", err)
	}

	// Initialize logger with timestamped file in the artifacts directory
	timestamp := time.Now().Format("2006-01-02-150405")
	logFile := filepath.Join(database.OutputDir, fmt.Sprintf("kpi-%s.log", timestamp))
	logF, err := logger.InitLogger(logFile)
	if err != nil {
		return fmt.Errorf("failed to initialize logger: %w", err)
	}
	defer func() {
		if err := logF.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close log file: %v\n", err)
		}
	}()
	fmt.Printf("Log file: %s\n", logFile)

	log.Println("KPI Collector initialized")

	// Apply prom YAML settings to flags before kubeconfig auth so the token
	// duration uses the correct once/duration values.
	if flags.TasksConfig != "" {
		spec, loadErr := config.LoadTasksSpec(flags.TasksConfig)
		if loadErr != nil {
			return loadErr
		}
		if spec.Prometheus != nil {
			config.ApplyPromTaskConfig(&flags, spec.Prometheus)
		}
	}

	if err := setupKubeconfigAuthIfNeeded(&flags); err != nil {
		return err
	}

	// Resolve tasks after kubeconfig auth so PromKPITask captures the
	// discovered ThanosURL and BearerToken.
	var tasks []task.Task
	var parallel, failFast bool
	if flags.TasksConfig != "" {
		tasks, parallel, failFast, err = resolveTasksFromSpec(&flags)
	} else {
		tasks, parallel, failFast, err = resolvePromKPIFromFlags(cmd, &flags)
	}
	if err != nil {
		return err
	}

	fmt.Printf("Database: %s\n", databaseLocation(flags))

	failedTasks := runAllTasks(cmd, tasks, parallel, failFast)

	absOutputDir, err := filepath.Abs(database.OutputDir)
	if err != nil {
		absOutputDir = database.OutputDir
	}
	fmt.Printf("Artifacts stored in: %s\n", absOutputDir)

	if flags.TarArtifacts {
		tarPath, tarErr := tarArtifactsDir(database.OutputDir)
		if tarErr != nil {
			return fmt.Errorf("failed to archive artifacts: %w", tarErr)
		}
		fmt.Printf("Artifacts archive: %s\n", tarPath)
	}

	if len(failedTasks) > 0 {
		return fmt.Errorf("tasks failed: %s", strings.Join(failedTasks, ", "))
	}

	return nil
}

// changedPromFlags returns the set of Prometheus override CLI flags that
// the user explicitly set on the command line.
func changedPromFlags(cmd *cobra.Command) map[string]bool {
	changed := make(map[string]bool, len(promOverrideFlags))
	for _, name := range promOverrideFlags {
		if cmd.Flags().Changed(name) {
			changed[name] = true
		}
	}
	return changed
}

// setupKubeconfigAuthIfNeeded discovers Thanos and a token before tasks are
// resolved, so PromKPITask is constructed with those credentials.
func setupKubeconfigAuthIfNeeded(flags *config.InputFlags) error {
	if flags.Kubeconfig == "" {
		return nil
	}

	log.Printf("Using kubeconfig authentication: %s", flags.Kubeconfig)

	tokenDuration := tokenDurationForCollection(flags.SingleRun, flags.Duration)

	thanosURL, token, err := kubernetes.SetupKubeconfigAuth(flags.Kubeconfig, tokenDuration)
	if err != nil {
		return fmt.Errorf("failed to setup kubeconfig auth: %w", err)
	}

	flags.ThanosURL = thanosURL
	flags.BearerToken = token
	fmt.Printf("Discovered Thanos URL: %s\n", flags.ThanosURL)
	fmt.Printf("Created service account token (sa=%s, ns=%s, expiry=%s)\n",
		kubernetes.TokenServiceAccountName,
		kubernetes.MonitoringNamespace,
		tokenDuration)

	return nil
}

func resolveTasksFromSpec(flags *config.InputFlags) ([]task.Task, bool, bool, error) {
	spec, err := config.LoadTasksSpec(flags.TasksConfig)
	if err != nil {
		return nil, false, false, err
	}
	log.Printf("Loaded tasks from %s (mode=%s, on-failure=%s)",
		flags.TasksConfig, spec.Orchestration.Mode, spec.Orchestration.OnFailure)

	// Apply YAML prom settings → flags (idempotent if already applied
	// before kubeconfig auth for token duration).
	if spec.Prometheus != nil {
		config.ApplyPromTaskConfig(flags, spec.Prometheus)
	}

	if err := config.ValidatePromSettings(*flags); err != nil {
		return nil, false, false, fmt.Errorf("prometheus config: %w", err)
	}

	var cpus *config.CPUPlaceholders
	if spec.Prometheus != nil {
		kpis, err := task.LoadPrometheusKPIs(spec)
		if err != nil {
			return nil, false, false, err
		}
		kpis, cpus, err = prepareLoadedKPIs(kpis, *flags)
		if err != nil {
			return nil, false, false, err
		}
		spec.Prometheus.Kpis = kpis.Queries
		spec.Prometheus.ConfigFile = ""
	}

	if spec.PerNodeData != nil && spec.PerNodeData.Isolcpus == "" && cpus == nil {
		cpus, err = fetchCPUPlaceholders(flags.Kubeconfig)
		if err != nil {
			return nil, false, false, err
		}
	}
	if err := setPerNodeDataIsolcpus(&spec, cpus); err != nil {
		return nil, false, false, err
	}

	tasks, err := task.ResolveFromTasksSpec(spec, *flags)
	if err != nil {
		return nil, false, false, err
	}

	parallel, failFast := orchestrationFromSpec(spec, *flags)
	return tasks, parallel, failFast, nil
}

// resolvePromKPIFromFlags is the legacy --prom-kpis-config / --kpis-file path:
// it always produces a single prometheus task. Other task types are only
// selected via --tasks.
func resolvePromKPIFromFlags(cmd *cobra.Command, flags *config.InputFlags) ([]task.Task, bool, bool, error) {
	kpis, err := setupPromKPIs(cmd, flags)
	if err != nil {
		return nil, false, false, err
	}

	tasks, err := task.FromPromKPIsFlag(*flags, kpis)
	if err != nil {
		return nil, false, false, err
	}
	return tasks, flags.Parallel, false, nil
}

// orchestrationFromSpec uses the tasks file, then ORs in CLI --parallel.
// Fail-fast comes from orchestration.on-failure (there is no CLI flag).
func orchestrationFromSpec(spec config.TasksSpec, flags config.InputFlags) (parallel, failFast bool) {
	return flags.Parallel || spec.Orchestration.Mode == config.ModeParallel,
		spec.Orchestration.OnFailure == config.OnFailureFailFast
}

// setupPromKPIs loads, validates, and prepares Prom KPI queries.
// YAML prom settings are applied as defaults; CLI flags override them.
func setupPromKPIs(cmd *cobra.Command, flags *config.InputFlags) (config.KPIs, error) {
	kpis, err := config.LoadKPIs(flags.PromKPIsConfig)
	if err != nil {
		return config.KPIs{}, fmt.Errorf("failed to load KPI queries: %w", err)
	}
	log.Printf("Loaded KPIs from %s", flags.PromKPIsConfig)

	config.ApplyKPIsDefaults(flags, kpis, changedPromFlags(cmd))

	if err := config.ValidatePromSettings(*flags); err != nil {
		return config.KPIs{}, err
	}

	kpis, _, err = prepareLoadedKPIs(kpis, *flags)
	return kpis, err
}

func prepareLoadedKPIs(kpis config.KPIs, flags config.InputFlags) (config.KPIs, *config.CPUPlaceholders, error) {
	if validationErrors := config.ValidateKPIs(kpis); len(validationErrors) > 0 {
		fmt.Println("KPI validation errors:")
		for _, e := range validationErrors {
			fmt.Printf("  ✗ %v\n", e)
		}
		return config.KPIs{}, nil, fmt.Errorf("found %d KPI validation error(s)", len(validationErrors))
	}
	fmt.Printf("✓ Validated %d KPI(s)\n", len(kpis.Queries))

	if !flags.SkipPrompts {
		if abort := promptIfManyUncategorized(kpis); abort {
			return config.KPIs{}, nil, fmt.Errorf("aborted by user")
		}
	}

	kpis, cpus, err := substituteCPUsIfNeeded(kpis, flags)
	if err != nil {
		return config.KPIs{}, nil, err
	}

	if !flags.SingleRun {
		warnFrequencyExceedsDuration(kpis, flags)

		if err := config.ValidateRangeFrequency(kpis, flags.SamplingFreq); err != nil {
			return config.KPIs{}, nil, err
		}
	}

	return kpis, cpus, nil
}

// runAllTasks executes tasks and returns the names of any that failed.
// parallel: run concurrently instead of sequentially.
// failFast: stop on first failure (sequential only; ignored when parallel).
func runAllTasks(cmd *cobra.Command, tasks []task.Task, parallel, failFast bool) []string {
	var mu sync.Mutex
	var failedTasks []string
	var wg sync.WaitGroup

	run := func(t task.Task) {
		defer wg.Done()
		output.PrintTaskStart(t.Name())
		if err := t.Run(cmd.Context()); err != nil {
			output.PrintTaskFailed(t.Name(), err)
			mu.Lock()
			failedTasks = append(failedTasks, t.Name())
			mu.Unlock()
			return
		}
		output.PrintTaskDone(t.Name())
	}

	for _, t := range tasks {
		wg.Add(1)
		if parallel {
			go run(t)
		} else {
			run(t)
			if failFast && len(failedTasks) > 0 {
				break
			}
		}
	}

	wg.Wait()
	return failedTasks
}

func databaseLocation(flags config.InputFlags) string {
	if flags.DatabaseType == "postgres" {
		return "postgres (external)"
	}
	return fmt.Sprintf("sqlite (%s)", filepath.Join(database.OutputDir, database.DefaultDBFileName))
}

// tokenDurationForCollection returns the token expiration to use when creating
// a service-account token via kubeconfig.
//
// For periodic collection the token covers the full duration plus a buffer.
// For single-run mode a fixed 1-hour window is used — generous enough for
// heavy range queries while still short-lived.
func tokenDurationForCollection(isSingleRun bool, collectionDuration time.Duration) time.Duration {
	const buffer = 10 * time.Minute

	if isSingleRun {
		return 1 * time.Hour
	}
	return collectionDuration + buffer
}

// substituteCPUsIfNeeded fetches PerformanceProfile CPU sets when queries contain
// {{RESERVED_CPUS}} or {{ISOLATED_CPUS}} and substitutes them into the queries.
func substituteCPUsIfNeeded(kpis config.KPIs, flags config.InputFlags) (config.KPIs, *config.CPUPlaceholders, error) {
	if !config.RequiresCPUSubstitution(kpis) {
		return kpis, nil, nil
	}

	cpus, err := fetchCPUPlaceholders(flags.Kubeconfig)
	if err != nil {
		return kpis, nil, err
	}
	return config.SubstituteCPUPlaceholders(kpis, cpus), cpus, nil
}

// fetchCPUPlaceholders reads reserved and isolated CPU sets from PerformanceProfiles.
func fetchCPUPlaceholders(kubeconfig string) (*config.CPUPlaceholders, error) {
	if kubeconfig == "" {
		return nil, fmt.Errorf("--kubeconfig is required to read PerformanceProfile CPU sets")
	}

	reservedCPUs, isolatedCPUs, err := kubernetes.FetchCPUsFromPerformanceProfiles(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch CPUs from PerformanceProfiles: %w", err)
	}

	fmt.Printf("Loaded CPU sets - Reserved: [%s], Isolated: [%s]\n", reservedCPUs, isolatedCPUs)
	return &config.CPUPlaceholders{Reserved: reservedCPUs, Isolated: isolatedCPUs}, nil
}

// setPerNodeDataIsolcpus copies PerformanceProfile CPU sets onto per-node-data
// when isolcpus was not set in the tasks file. An explicit value is left as-is.
func setPerNodeDataIsolcpus(spec *config.TasksSpec, cpus *config.CPUPlaceholders) error {
	if spec.PerNodeData == nil || spec.PerNodeData.Isolcpus != "" {
		return nil
	}
	if cpus == nil || cpus.Isolated == "" {
		return fmt.Errorf("%s: PerformanceProfile isolated CPU set is empty", config.TaskConfigPerNodeData)
	}

	spec.PerNodeData.Isolcpus = strings.ReplaceAll(cpus.Isolated, "|", ",")
	log.Printf("%s: CPU sets from PerformanceProfile: reserved=%s isolated=%s",
		config.TaskConfigPerNodeData,
		strings.ReplaceAll(cpus.Reserved, "|", ","),
		spec.PerNodeData.Isolcpus)
	return nil
}

const uncategorizedThreshold = 15

// promptIfManyUncategorized asks the user for confirmation when 15+ KPIs
// have no category set. Returns true if the user chose to abort.
func promptIfManyUncategorized(kpis config.KPIs) bool {
	uncategorized := 0
	for _, q := range kpis.Queries {
		if q.Category == "" {
			uncategorized++
		}
	}

	if uncategorized < uncategorizedThreshold {
		return false
	}

	fmt.Fprintf(os.Stderr,
		"\n⚠  %d KPIs detected without categories.\n"+
			"   Without categories, all data is stored in a single table which\n"+
			"   degrades query performance at scale.\n\n"+
			"   Proceed anyway? [y/N] ",
		uncategorized)

	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))

	return answer != "y" && answer != "yes"
}

// warnFrequencyExceedsDuration prints a warning if any KPI's sampling frequency
// is longer than the total duration, meaning only one sample will be collected
func warnFrequencyExceedsDuration(kpis config.KPIs, flags config.InputFlags) {
	for _, kpi := range kpis.Queries {
		if kpi.IsRunOnce() {
			continue
		}

		effectiveFreq := kpi.GetEffectiveFrequency(flags.SamplingFreq)

		if effectiveFreq > flags.Duration {
			fmt.Printf("WARNING: KPI '%s' has frequency %s which exceeds duration %s. Only 1 sample will be collected.\n",
				kpi.ID, effectiveFreq, flags.Duration)
		}
	}

	// Also warn about the default frequency if no custom frequencies are set
	if flags.SamplingFreq > flags.Duration {
		fmt.Printf("WARNING: Default sampling frequency %s exceeds duration %s. KPIs without custom frequency will only collect 1 sample.\n",
			flags.SamplingFreq, flags.Duration)
	}
}
