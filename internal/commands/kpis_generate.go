package commands

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/config"
	kpiprofiles "github.com/redhat-best-practices-for-k8s/kpi-collection-tool/prom-kpi-profiles"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type domain struct {
	Name string
	IDs  []string
}

type profile struct {
	Name    string
	File    string
	Domains []domain
}

var profiles = map[string]profile{
	"ran":  ranProfile(),
	"core": coreProfile(),
	"hub":  hubProfile(),
}

var kpisGenerateFlags struct {
	profile       string
	file          string
	all           bool
	overwrite     bool
	uncategorized bool
}

var kpisGenerateCmd = &cobra.Command{
	Use:   "generate --profile <profile>",
	Short: "Generate a KPI configuration file for a cluster profile",
	Long: `Generate a YAML KPI configuration file with queries tailored for a specific cluster profile.

Supported profiles: ran, core, hub

In interactive mode (default), you will be prompted to select which KPI
categories to include. Use --all to include all categories without prompts.

By default, each KPI includes a category field that routes metrics into
per-category database tables for better query performance. Use --uncategorized
to omit category fields and store all metrics in a single table.`,
	Example: `  # Generate all RAN KPIs
  kpi-collector kpis generate --profile ran --all

  # Interactively select categories for a Core cluster
  kpi-collector kpis generate --profile core -f core-kpis.yaml

  # Generate Hub KPIs to a custom path
  kpi-collector kpis generate --profile hub --all -f /path/to/hub-kpis.yaml

  # Generate without category fields (single-table storage)
  kpi-collector kpis generate --profile ran --all --uncategorized`,
	Args: cobra.NoArgs,
	RunE: runKpisGenerate,
}

func init() {
	kpisCmd.AddCommand(kpisGenerateCmd)

	kpisGenerateCmd.Flags().StringVarP(&kpisGenerateFlags.profile, "profile", "p", "",
		fmt.Sprintf("cluster profile (%s)", strings.Join(config.ValidClusterTypes, ", ")))
	kpisGenerateCmd.Flags().StringVarP(&kpisGenerateFlags.file, "file", "f", "",
		"output file path (default: <profile>-kpis.yaml)")
	kpisGenerateCmd.Flags().BoolVar(&kpisGenerateFlags.all, "all", false,
		"include all KPI categories without prompts")
	kpisGenerateCmd.Flags().BoolVar(&kpisGenerateFlags.overwrite, "overwrite", false,
		"overwrite the output file if it already exists")
	kpisGenerateCmd.Flags().BoolVar(&kpisGenerateFlags.uncategorized, "uncategorized", false,
		"generate KPIs without category fields (all data stored in a single table)")

	_ = kpisGenerateCmd.MarkFlagRequired("profile")
}

func runKpisGenerate(_ *cobra.Command, _ []string) error {
	profileName := kpisGenerateFlags.profile

	prof, ok := profiles[profileName]
	if !ok {
		return fmt.Errorf("unknown profile %q, valid profiles: %s",
			profileName, strings.Join(config.ValidClusterTypes, ", "))
	}

	filePath := resolveOutputFile(profileName)

	if err := validateOutputPath(filePath, kpisGenerateFlags.overwrite); err != nil {
		return fmt.Errorf("failed to validate output path: %w", err)
	}

	allKPIs, err := loadProfileKPIs(prof)
	if err != nil {
		return fmt.Errorf("failed to load profile KPIs: %w", err)
	}

	queries, err := selectQueries(prof, allKPIs.Queries, kpisGenerateFlags.all)
	if err != nil {
		return fmt.Errorf("failed to select queries: %w", err)
	}

	if len(queries) == 0 {
		fmt.Println("No KPI categories selected. No file generated.")
		return nil
	}

	if kpisGenerateFlags.uncategorized {
		queries = stripCategories(queries)
	}

	return writeKPIsFile(filePath, queries)
}

// ---------------------------------------------------------------------------
// File helpers
// ---------------------------------------------------------------------------

func resolveOutputFile(profileName string) string {
	if kpisGenerateFlags.file != "" {
		return kpisGenerateFlags.file
	}
	return profileName + "-kpis.yaml"
}

func validateOutputPath(filePath string, overwrite bool) error {
	if info, err := os.Stat(filePath); err == nil { // if err==nil then path exists
		if info.IsDir() {
			return fmt.Errorf("%q is a directory, not a file path", filePath)
		}
		if !overwrite {
			return fmt.Errorf("file %q already exists (use --overwrite to replace it)", filePath)
		}
	}

	dir := filepath.Dir(filePath)
	if dir == "." {
		return nil
	}

	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("directory %q does not exist", dir)
	}
	if !info.IsDir() {
		return fmt.Errorf("%q is not a directory", dir)
	}

	return nil
}

func writeKPIsFile(filePath string, queries []config.Query) error {
	kpisData := config.KPIs{Queries: queries}

	data, err := yaml.Marshal(kpisData)
	if err != nil {
		return fmt.Errorf("failed to marshal KPIs: %w", err)
	}

	// Add blank lines between KPI entries for readability
	formatted := strings.ReplaceAll(string(data), "\n  - id:", "\n\n  - id:")
	data = []byte(formatted)

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return fmt.Errorf("failed to resolve absolute path: %w", err)
	}

	if err := os.WriteFile(absPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write file %q: %w", absPath, err)
	}

	fmt.Printf("Generated %s with %d KPIs\n", absPath, len(queries))
	fmt.Println("You can edit this file to customize each KPI. Per-query options:")
	fmt.Println("  sample-frequency  Override the global collection frequency (e.g. 2m, 30s)")
	fmt.Println("  run-once          Set to true to collect a KPI only once")
	fmt.Println("  query-type        instant (default) or range for time-window queries")
	fmt.Println("  range             Required when query-type is range (nested: step, since, until)")
	return nil
}

func stripCategories(queries []config.Query) []config.Query {
	stripped := make([]config.Query, len(queries))
	copy(stripped, queries)
	for i := range stripped {
		stripped[i].Category = ""
	}
	return stripped
}

// ---------------------------------------------------------------------------
// Profile loading and filtering
// ---------------------------------------------------------------------------

func loadProfileKPIs(prof profile) (config.KPIs, error) {
	data, err := kpiprofiles.FS.ReadFile(prof.File)
	if err != nil {
		return config.KPIs{}, fmt.Errorf("failed to read embedded profile %q: %w", prof.File, err)
	}

	var kpis config.KPIs
	if err := yaml.Unmarshal(data, &kpis); err != nil {
		return config.KPIs{}, fmt.Errorf("failed to parse profile %q: %w", prof.File, err)
	}

	return kpis, nil
}

func selectQueries(prof profile, allQueries []config.Query, all bool) ([]config.Query, error) {
	if all {
		return allQueries, nil
	}
	return promptForDomains(prof, allQueries)
}

func promptForDomains(prof profile, allQueries []config.Query) ([]config.Query, error) {
	reader := bufio.NewReader(os.Stdin)
	var selectedDomains []domain

	for _, d := range prof.Domains {
		selected, err := promptYesNo(reader, d.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to read input: %w", err)
		}
		if selected {
			selectedDomains = append(selectedDomains, d)
		}
	}

	return filterByDomains(allQueries, selectedDomains), nil
}

func filterByDomains(allQueries []config.Query, domains []domain) []config.Query {
	allowed := make(map[string]bool)
	for _, d := range domains {
		for _, id := range d.IDs {
			allowed[id] = true
		}
	}

	var filtered []config.Query
	for _, q := range allQueries {
		if allowed[q.ID] {
			filtered = append(filtered, q)
		}
	}
	return filtered
}

// ---------------------------------------------------------------------------
// Interactive prompt
// ---------------------------------------------------------------------------

func promptYesNo(reader *bufio.Reader, domainName string) (bool, error) {
	for {
		fmt.Printf("Do you want %s kpis? [y/n]: ", domainName)

		input, err := reader.ReadString('\n')
		if err != nil {
			return false, err
		}

		switch strings.TrimSpace(strings.ToLower(input)) {
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		default:
			fmt.Println("Please answer y or n.")
		}
	}
}

// ---------------------------------------------------------------------------
// Profile definitions — domain groupings reference KPI IDs from the YAML files
// ---------------------------------------------------------------------------

func ranProfile() profile {
	return profile{
		Name: "ran",
		File: kpiprofiles.RAN,
		Domains: []domain{
			{Name: "CPU & Resource Isolation", IDs: []string{
				"cpu-reserved-cores", "cpu-isolated-cores", "cpu-node-total",
				"cpu-system-slice", "cpu-ovs-slice", "cpu-pods-average",
			}},
			{Name: "Memory & HugePages", IDs: []string{
				"memory-node-used-percentage", "memory-working-set-by-pod", "memory-rss-by-pod",
				"hugepages-1g-free", "hugepages-1g-total", "hugepages-2m-free", "hugepages-2m-total",
			}},
			{Name: "PTP / Timing", IDs: []string{
				"ptp-offset-master", "ptp-max-offset-master",
				"ptp-clock-state", "ptp-interface-role",
			}},
			{Name: "Networking & OVN", IDs: []string{
				"network-node-rx-bytes", "network-node-tx-bytes",
				"network-node-rx-errors", "network-node-tx-errors",
				"network-container-rx-bytes", "network-container-tx-bytes",
				"ovn-controller-cpu", "ovn-controller-memory",
			}},
			{Name: "System & Storage", IDs: []string{
				"disk-io-read-bytes", "disk-io-write-bytes", "disk-usage-percentage",
				"node-context-switches", "node-interrupts", "pod-restart-count",
			}},
		},
	}
}

func coreProfile() profile {
	return profile{
		Name: "core",
		File: kpiprofiles.Core,
		Domains: []domain{
			{Name: "Control Plane & etcd", IDs: []string{
				"apiserver-request-duration-99p", "apiserver-request-rate", "apiserver-error-rate",
				"etcd-db-size", "etcd-disk-wal-fsync-duration-99p", "etcd-leader-changes",
			}},
			{Name: "Node & Pod Resources", IDs: []string{
				"cpu-node-total", "cpu-usage-by-namespace",
				"memory-node-used-percentage", "memory-usage-by-namespace",
				"node-load-1min", "node-load-5min",
				"pod-restart-count", "pod-status-not-ready", "cluster-uptime",
			}},
			{Name: "Networking & Ingress", IDs: []string{
				"network-node-rx-bytes", "network-node-tx-bytes", "ingress-request-rate",
			}},
			{Name: "Storage", IDs: []string{
				"disk-usage-percentage", "pv-usage-percentage",
				"disk-io-read-bytes", "disk-io-write-bytes",
			}},
		},
	}
}

func hubProfile() profile {
	return profile{
		Name: "hub",
		File: kpiprofiles.Hub,
		Domains: []domain{
			{Name: "Control Plane & etcd", IDs: []string{
				"apiserver-request-duration-99p", "apiserver-request-rate", "apiserver-error-rate",
				"etcd-db-size", "etcd-disk-wal-fsync-duration-99p", "etcd-leader-changes",
			}},
			{Name: "ACM & GitOps", IDs: []string{
				"acm-cpu-usage", "acm-memory-usage",
				"gitops-cpu-usage", "gitops-memory-usage",
				"acm-managed-clusters", "acm-policy-noncompliant",
			}},
			{Name: "Node & Pod Resources", IDs: []string{
				"cpu-node-total", "memory-node-used-percentage", "node-load-1min",
				"pod-restart-count", "pod-status-not-ready", "cluster-uptime",
			}},
			{Name: "Networking & Storage", IDs: []string{
				"network-node-rx-bytes", "network-node-tx-bytes",
				"disk-usage-percentage", "pv-usage-percentage",
			}},
		},
	}
}
