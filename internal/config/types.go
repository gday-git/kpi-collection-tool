// Package config provides configuration types, validation, and loading
// for the KPI collector. It defines input flags, KPI query structures,
// and validation logic for command-line arguments.
package config

import (
	"fmt"
	"strings"
	"time"
)

// Duration is a wrapper around time.Duration that supports YAML unmarshaling
// from both duration strings (e.g., "30s", "2m", "1h") and integer seconds
type Duration struct {
	time.Duration
}

// UnmarshalYAML implements yaml.Unmarshaler for Duration.
// Supports both string format ("30s", "2m", "1h") and integer seconds (60).
func (d *Duration) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var v interface{}
	if err := unmarshal(&v); err != nil {
		return err
	}

	switch value := v.(type) {
	case int:
		d.Duration = time.Duration(value) * time.Second
		return nil
	case float64:
		d.Duration = time.Duration(value) * time.Second
		return nil
	case string:
		var err error
		d.Duration, err = time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("invalid duration string %q: %w", value, err)
		}
		return nil
	default:
		return fmt.Errorf("invalid duration type: %T", v)
	}
}

// MarshalYAML implements yaml.Marshaler for Duration
func (d Duration) MarshalYAML() (interface{}, error) {
	return d.String(), nil
}

// TimeRef represents a flexible time specification that can be either a Go
// duration string (e.g. "2h", "1m30s") interpreted as relative to "now", or an
// absolute RFC 3339 time reference (e.g. "2026-04-07T12:24:25Z").
type TimeRef struct {
	duration *time.Duration
	absolute *time.Time
}

// IsDuration returns true when the time reference holds a relative duration.
func (t *TimeRef) IsDuration() bool { return t.duration != nil }

// IsAbsolute returns true when the time reference holds an absolute point in time.
func (t *TimeRef) IsAbsolute() bool { return t.absolute != nil }

// DurationValue returns the contained duration. Panics if not a duration.
func (t *TimeRef) DurationValue() time.Duration { return *t.duration }

// AbsoluteValue returns the contained time. Panics if not absolute.
func (t *TimeRef) AbsoluteValue() time.Time { return *t.absolute }

// Resolve converts the TimeRef to an absolute time.Time.
// Durations are subtracted from the supplied reference time (typically time.Now()).
func (t *TimeRef) Resolve(now time.Time) time.Time {
	if t.absolute != nil {
		return *t.absolute
	}
	return now.Add(-*t.duration)
}

// UnmarshalYAML implements yaml.Unmarshaler for TimeRef.
// Accepts either a Go duration string ("2h", "30m") or an RFC 3339 timestamp.
func (t *TimeRef) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return fmt.Errorf("time reference must be a string: %w", err)
	}

	s = strings.TrimSpace(s)

	if d, err := time.ParseDuration(s); err == nil {
		t.duration = &d
		return nil
	}

	if parsed, err := time.Parse(time.RFC3339, s); err == nil {
		t.absolute = &parsed
		return nil
	}

	return fmt.Errorf("invalid time reference %q: must be a Go duration (e.g. \"2h\") or RFC 3339 format (e.g. \"2026-04-07T12:24:25Z\")", s)
}

// MarshalYAML implements yaml.Marshaler for TimeRef
func (t TimeRef) MarshalYAML() (interface{}, error) {
	if t.duration != nil {
		return t.duration.String(), nil
	}
	if t.absolute != nil {
		return t.absolute.Format(time.RFC3339), nil
	}
	return nil, nil
}

// RangeWindow defines the time window for a range query.
// Step is always required. Since is required; Until is optional (defaults to "now").
// Both Since and Until accept either a Go duration ("2h") or an RFC 3339 timestamp.
type RangeWindow struct {
	Step  *Duration `yaml:"step"`
	Since *TimeRef  `yaml:"since"`
	Until *TimeRef  `yaml:"until,omitempty"`
}

// InputFlags holds all command line flag values
type InputFlags struct {
	BearerToken    string
	ThanosURL      string
	Kubeconfig     string
	ClusterName    string
	ClusterType    string
	InsecureTLS    bool
	SamplingFreq   time.Duration
	Duration       time.Duration
	DatabaseType   string // "sqlite" or "postgres"
	PostgresURL    string // PostgreSQL connection string
	PromKPIsConfig string // path to Prom KPI config file (--prom-kpis-config / --kpis-file)
	TasksConfig    string // path to --tasks YAML file or directory containing tasks.yaml
	SingleRun      bool   // collect metrics once and exit
	Parallel       bool   // run tasks concurrently instead of sequentially
	SkipPrompts    bool   // skip interactive prompts (--yes/-y)
	TarArtifacts   bool   // create .tar.gz of artifacts dir after run
}

// HasAnyTaskConfig returns true if at least one task config flag is set.
func (f InputFlags) HasAnyTaskConfig() bool {
	return f.PromKPIsConfig != "" || f.TasksConfig != ""
}

// Query represents a single KPI query configuration
type Query struct {
	ID              string       `yaml:"id"`
	PromQuery       string       `yaml:"promquery"`
	Category        string       `yaml:"category,omitempty"`
	SampleFrequency *Duration    `yaml:"sample-frequency,omitempty"`
	QueryType       string       `yaml:"query-type,omitempty"`
	Range           *RangeWindow `yaml:"range,omitempty"`
	RunOnce         *bool        `yaml:"run-once,omitempty"`
}

// IsRunOnce returns true if this query is configured to run only once
func (q *Query) IsRunOnce() bool {
	return q.RunOnce != nil && *q.RunOnce
}

// KPIs represents the structure of the KPI configuration file containing
// the list of KPI queries to be executed against Prometheus/Thanos.
//
// When loaded via --prom-kpis-config, the optional sampling/database fields
// serve as defaults that CLI flags can override.
type KPIs struct {
	Queries     []Query   `yaml:"kpis"`
	Frequency   *Duration `yaml:"frequency,omitempty"`
	Duration    *Duration `yaml:"duration,omitempty"`
	DBType      string    `yaml:"dbType,omitempty"`
	PostgresURL string    `yaml:"postgresURL,omitempty"`
	Once        *bool     `yaml:"once,omitempty"`
}

// GetEffectiveFrequency returns the sample frequency for this query,
// falling back to the provided default if not specified
func (q *Query) GetEffectiveFrequency(defaultFreq time.Duration) time.Duration {
	if q.SampleFrequency != nil && q.SampleFrequency.Duration > 0 {
		return q.SampleFrequency.Duration
	}
	return defaultFreq
}

// GetEffectiveQueryType returns the query type for this query,
// defaulting to "instant" if not specified
func (q *Query) GetEffectiveQueryType() string {
	if qt := strings.TrimSpace(q.QueryType); qt != "" {
		return qt
	}
	return "instant"
}
