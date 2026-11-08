package config

import (
	"fmt"
	"strings"
	"time"
)

// ValidClusterTypes lists all accepted cluster type values.
var ValidClusterTypes = []string{"ran", "core", "hub"}

// IsValidClusterType reports whether t is a recognised cluster type.
func IsValidClusterType(t string) bool {
	for _, v := range ValidClusterTypes {
		if t == v {
			return true
		}
	}
	return false
}

// clusterTypeList formats the valid cluster types for error messages
// using an Oxford-comma style ("'ran', 'core', or 'hub'").
func clusterTypeList() string {
	quoted := make([]string, len(ValidClusterTypes))
	for i, t := range ValidClusterTypes {
		quoted[i] = "'" + t + "'"
	}
	if len(quoted) <= 1 {
		return strings.Join(quoted, "")
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + ", or " + quoted[len(quoted)-1]
}

// ValidateClusterType returns an error when t is empty or not recognised.
func ValidateClusterType(t string) error {
	if t == "" {
		return fmt.Errorf("cluster-type is required: must be %s", clusterTypeList())
	}
	if !IsValidClusterType(t) {
		return fmt.Errorf("invalid cluster-type '%s': must be %s", t, clusterTypeList())
	}
	return nil
}

// ValidateAuth checks that exactly one authentication method is provided:
// either (token + thanos-url) or kubeconfig.
func ValidateAuth(token, thanosURL, kubeconfig string) error {
	valid := (token != "" && thanosURL != "" && kubeconfig == "") ||
		(token == "" && thanosURL == "" && kubeconfig != "")
	if !valid {
		return fmt.Errorf("invalid flag combination: either provide --token and --thanos-url, or provide --kubeconfig")
	}
	return nil
}

// ValidateTaskConfig checks that exactly one task config source is set.
func ValidateTaskConfig(tasksConfig, promKPIsConfig string) error {
	if tasksConfig != "" && promKPIsConfig != "" {
		return fmt.Errorf("--tasks and --prom-kpis-config are mutually exclusive")
	}
	if tasksConfig == "" && promKPIsConfig == "" {
		return fmt.Errorf("either --tasks or --prom-kpis-config is required")
	}
	return nil
}

// ValidateFlags ensures the correct combination of flags is provided.
// Prometheus sampling fields (frequency, duration) and database settings
// (db-type, postgres-url) are validated separately by ValidatePromSettings
// after YAML values have been merged into the flags.
func ValidateFlags(flags InputFlags) error {
	if flags.ClusterName == "" {
		return fmt.Errorf("cluster name is required: use --cluster-name flag")
	}

	if err := ValidateClusterType(flags.ClusterType); err != nil {
		return err
	}

	if flags.InsecureTLS {
		fmt.Println("WARNING: TLS certificate verification is disabled. Use only in development environments.")
	}

	if err := ValidateAuth(flags.BearerToken, flags.ThanosURL, flags.Kubeconfig); err != nil {
		return err
	}

	return ValidateTaskConfig(flags.TasksConfig, flags.PromKPIsConfig)
}

// ValidateDatabaseType checks db-type and postgres-url consistency.
func ValidateDatabaseType(dbType, postgresURL string) error {
	if dbType != "sqlite" && dbType != "postgres" {
		return fmt.Errorf("invalid db-type: must be 'sqlite' or 'postgres'")
	}
	if dbType == "postgres" && postgresURL == "" {
		return fmt.Errorf("postgres-url is required when db-type=postgres")
	}
	return nil
}

// ValidatePromSettings checks the Prometheus-specific fields in InputFlags.
// Called after YAML values (from tasks.yaml or KPI YAML) have been applied.
func ValidatePromSettings(flags InputFlags) error {
	if flags.SamplingFreq <= 0 {
		return fmt.Errorf("sampling frequency must be greater than 0")
	}

	if flags.Duration <= 0 {
		return fmt.Errorf("duration must be greater than 0")
	}

	return ValidateDatabaseType(flags.DatabaseType, flags.PostgresURL)
}

// ApplyPromTaskConfig copies Prometheus sampling settings from a tasks.yaml
// prometheus section into InputFlags. Used with --tasks where CLI
// --frequency/--duration/--once are rejected and the YAML is the sole source.
// Database settings are not applied here; they remain global CLI flags.
func ApplyPromTaskConfig(flags *InputFlags, p *PrometheusTaskConfig) {
	if p == nil {
		return
	}
	if p.Frequency != nil {
		flags.SamplingFreq = p.Frequency.Duration
	}
	if p.Duration != nil {
		flags.Duration = p.Duration.Duration
	}
	if p.Once != nil {
		flags.SingleRun = *p.Once
	}
}

// ApplyKPIsDefaults copies Prometheus settings from a KPI YAML file into
// InputFlags, but only for fields not already set by CLI (indicated by
// changedFlags). Used with --prom-kpis-config where CLI flags override YAML.
func ApplyKPIsDefaults(flags *InputFlags, kpis KPIs, changedFlags map[string]bool) {
	if kpis.Frequency != nil && !changedFlags["frequency"] {
		flags.SamplingFreq = kpis.Frequency.Duration
	}
	if kpis.Duration != nil && !changedFlags["duration"] {
		flags.Duration = kpis.Duration.Duration
	}
	if kpis.DBType != "" && !changedFlags["db-type"] {
		flags.DatabaseType = kpis.DBType
	}
	if kpis.PostgresURL != "" && !changedFlags["postgres-url"] {
		flags.PostgresURL = kpis.PostgresURL
	}
	if kpis.Once != nil && !changedFlags["once"] {
		flags.SingleRun = *kpis.Once
	}
}

// ValidateRangeFrequency checks range queries with duration-based since lookback
// for frequency/range mismatches. Returns an error if frequency exceeds since
// (data gaps). Prints a warning for heavy overlap. Queries using absolute
// start/end are skipped since their window is fixed.
func ValidateRangeFrequency(kpis KPIs, samplingFreq time.Duration) error {
	for _, kpi := range kpis.Queries {
		if kpi.GetEffectiveQueryType() != "range" || kpi.Range == nil || kpi.Range.Since == nil {
			continue
		}

		if !kpi.Range.Since.IsDuration() {
			continue
		}

		freq := kpi.GetEffectiveFrequency(samplingFreq)
		since := kpi.Range.Since.DurationValue()

		if freq > since {
			return fmt.Errorf("KPI '%s' has frequency %s > since %s — this creates gaps where no data is collected",
				kpi.ID, freq, since)
		}

		if freq < since/2 {
			overlapPercent := 100 - (100*freq)/since
			fmt.Printf("WARNING: KPI '%s' has frequency %s with since %s — ~%d%% of each query overlaps the previous one.\n",
				kpi.ID, freq, since, overlapPercent)
		}
	}

	return nil
}
