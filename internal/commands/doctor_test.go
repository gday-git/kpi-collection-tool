package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/config"
	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/database"
)

var _ = Describe("doctor command", func() {
	var (
		tmpDir      string
		savedFlags  config.InputFlags
		savedOutDir string
	)

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "doctor-test-*")
		Expect(err).NotTo(HaveOccurred())

		savedFlags = doctorFlags
		savedOutDir = database.OutputDir
		database.OutputDir = tmpDir
	})

	AfterEach(func() {
		doctorFlags = savedFlags
		database.OutputDir = savedOutDir
		if tmpDir != "" {
			Expect(os.RemoveAll(tmpDir)).To(Succeed())
		}
	})

	writeFile := func(name, content string) string {
		p := filepath.Join(tmpDir, name)
		Expect(os.WriteFile(p, []byte(content), 0644)).To(Succeed())
		return p
	}

	validKPIsYAML := `kpis:
  - id: cpu-usage
    promquery: avg(rate(node_cpu_seconds_total[5m]))
  - id: mem-usage
    promquery: node_memory_MemTotal_bytes
`

	setValidFlags := func(kpisPath string) {
		doctorFlags = config.InputFlags{
			ClusterName:    "test-cluster",
			ClusterType:    "ran",
			Kubeconfig:     writeFile("kubeconfig", "dummy"),
			SamplingFreq:   60 * time.Second,
			Duration:       45 * time.Minute,
			DatabaseType:   "sqlite",
			PromKPIsConfig: kpisPath,
		}
	}

	Describe("appendClusterChecks", func() {
		It("should pass with valid cluster name and type", func() {
			doctorFlags.ClusterName = "my-cluster"
			doctorFlags.ClusterType = "ran"

			checks := appendClusterChecks(nil)

			Expect(checks).To(HaveLen(2))
			Expect(checks[0].status).To(Equal(statusPass))
			Expect(checks[0].message).To(ContainSubstring("my-cluster"))
			Expect(checks[1].status).To(Equal(statusPass))
			Expect(checks[1].message).To(ContainSubstring("ran"))
		})

		It("should skip checks when both are empty", func() {
			doctorFlags.ClusterName = ""
			doctorFlags.ClusterType = ""

			checks := appendClusterChecks(nil)

			Expect(checks).To(BeEmpty())
		})

		It("should warn when cluster name is missing but type is set", func() {
			doctorFlags.ClusterName = ""
			doctorFlags.ClusterType = "ran"

			checks := appendClusterChecks(nil)

			Expect(checks[0].status).To(Equal(statusWarn))
			Expect(checks[0].message).To(ContainSubstring("not set"))
		})

		It("should fail with invalid cluster type", func() {
			doctorFlags.ClusterName = "test"
			doctorFlags.ClusterType = "invalid"

			checks := appendClusterChecks(nil)

			Expect(checks).To(HaveLen(2))
			Expect(checks[1].status).To(Equal(statusFail))
			Expect(checks[1].message).To(ContainSubstring("Invalid cluster type"))
		})

		It("should warn when cluster type is empty but name is set", func() {
			doctorFlags.ClusterName = "test"
			doctorFlags.ClusterType = ""

			checks := appendClusterChecks(nil)

			Expect(checks[1].status).To(Equal(statusWarn))
			Expect(checks[1].message).To(ContainSubstring("not set"))
		})
	})

	Describe("appendAuthChecks", func() {
		It("should pass with kubeconfig", func() {
			doctorFlags.Kubeconfig = "/path/to/kubeconfig"

			checks := appendAuthChecks(nil)

			Expect(checks).To(HaveLen(1))
			Expect(checks[0].status).To(Equal(statusPass))
			Expect(checks[0].message).To(ContainSubstring("kubeconfig"))
		})

		It("should pass with token + thanos-url", func() {
			doctorFlags.BearerToken = "tok"
			doctorFlags.ThanosURL = "https://thanos"

			checks := appendAuthChecks(nil)

			Expect(checks).To(HaveLen(1))
			Expect(checks[0].status).To(Equal(statusPass))
			Expect(checks[0].message).To(ContainSubstring("token"))
		})

		It("should fail when both kubeconfig and token are set", func() {
			doctorFlags.Kubeconfig = "/path"
			doctorFlags.BearerToken = "tok"
			doctorFlags.ThanosURL = "https://thanos"

			checks := appendAuthChecks(nil)

			Expect(checks[0].status).To(Equal(statusFail))
			Expect(checks[0].message).To(ContainSubstring("both"))
		})

		It("should skip checks when no auth flags are set", func() {
			checks := appendAuthChecks(nil)

			Expect(checks).To(BeEmpty())
		})

		It("should fail with token but no thanos-url", func() {
			doctorFlags.BearerToken = "tok"

			checks := appendAuthChecks(nil)

			Expect(checks[0].status).To(Equal(statusFail))
			Expect(checks[0].message).To(ContainSubstring("without --thanos-url"))
		})

		It("should fail with thanos-url but no token", func() {
			doctorFlags.ThanosURL = "https://thanos"

			checks := appendAuthChecks(nil)

			Expect(checks[0].status).To(Equal(statusFail))
			Expect(checks[0].message).To(ContainSubstring("without --token"))
		})

		It("should warn when insecure-tls is set", func() {
			doctorFlags.Kubeconfig = "/path"
			doctorFlags.InsecureTLS = true

			checks := appendAuthChecks(nil)

			Expect(checks).To(HaveLen(2))
			Expect(checks[1].status).To(Equal(statusWarn))
			Expect(checks[1].message).To(ContainSubstring("insecure"))
		})
	})

	Describe("appendTaskConfigFlagChecks", func() {
		It("should warn when no config is specified", func() {
			checks := appendTaskConfigFlagChecks(nil)

			Expect(checks[0].status).To(Equal(statusWarn))
			Expect(checks[0].message).To(ContainSubstring("No task config"))
		})

		It("should fail when both are specified", func() {
			doctorFlags.PromKPIsConfig = "a.yaml"
			doctorFlags.TasksConfig = "b.yaml"

			checks := appendTaskConfigFlagChecks(nil)

			Expect(checks[0].status).To(Equal(statusFail))
			Expect(checks[0].message).To(ContainSubstring("mutually exclusive"))
		})

		It("should pass with prom-kpis-config", func() {
			doctorFlags.PromKPIsConfig = "kpis.yaml"

			checks := appendTaskConfigFlagChecks(nil)

			Expect(checks[0].status).To(Equal(statusPass))
			Expect(checks[0].message).To(ContainSubstring("prom-kpis-config"))
		})
	})

	Describe("appendSamplingChecks", func() {
		It("should pass with valid frequency and duration", func() {
			doctorFlags.SamplingFreq = 30 * time.Second
			doctorFlags.Duration = 10 * time.Minute

			checks := appendSamplingChecks(nil)

			Expect(checks).To(HaveLen(2))
			Expect(checks[0].status).To(Equal(statusPass))
			Expect(checks[1].status).To(Equal(statusPass))
		})

		It("should report single-run mode", func() {
			doctorFlags.SingleRun = true

			checks := appendSamplingChecks(nil)

			Expect(checks).To(HaveLen(1))
			Expect(checks[0].status).To(Equal(statusPass))
			Expect(checks[0].message).To(ContainSubstring("single-run"))
		})

		It("should fail with zero frequency", func() {
			doctorFlags.SamplingFreq = 0
			doctorFlags.Duration = 10 * time.Minute

			checks := appendSamplingChecks(nil)

			Expect(checks[0].status).To(Equal(statusFail))
		})
	})

	Describe("appendFileChecks", func() {
		It("should pass when referenced files exist", func() {
			kpis := writeFile("kpis.yaml", validKPIsYAML)
			kube := writeFile("kubeconfig", "dummy")
			doctorFlags.Kubeconfig = kube
			doctorFlags.PromKPIsConfig = kpis

			checks := appendFileChecks(nil)

			Expect(checks).To(HaveLen(2))
			Expect(checks[0].status).To(Equal(statusPass))
			Expect(checks[1].status).To(Equal(statusPass))
		})

		It("should fail when kubeconfig does not exist", func() {
			doctorFlags.Kubeconfig = "/nonexistent/kubeconfig"

			checks := appendFileChecks(nil)

			Expect(checks).To(HaveLen(1))
			Expect(checks[0].status).To(Equal(statusFail))
			Expect(checks[0].message).To(ContainSubstring("not found"))
		})

		It("should fail when KPI config does not exist", func() {
			doctorFlags.PromKPIsConfig = "/nonexistent/kpis.yaml"

			checks := appendFileChecks(nil)

			Expect(checks).To(HaveLen(1))
			Expect(checks[0].status).To(Equal(statusFail))
			Expect(checks[0].message).To(ContainSubstring("not found"))
		})

		It("should fail when tasks directory lacks tasks.yaml", func() {
			emptyDir, err := os.MkdirTemp(tmpDir, "empty-*")
			Expect(err).NotTo(HaveOccurred())
			doctorFlags.TasksConfig = emptyDir

			checks := appendFileChecks(nil)

			Expect(checks).To(HaveLen(1))
			Expect(checks[0].status).To(Equal(statusFail))
			Expect(checks[0].message).To(ContainSubstring("tasks.yaml not found"))
		})
	})

	Describe("appendPromKPIChecks", func() {
		It("should pass with valid KPI YAML", func() {
			kpis := writeFile("kpis.yaml", validKPIsYAML)
			doctorFlags.PromKPIsConfig = kpis
			doctorFlags.SamplingFreq = 60 * time.Second
			doctorFlags.Duration = 45 * time.Minute

			checks := appendPromKPIChecks(nil)

			passCount := 0
			for _, c := range checks {
				if c.status == statusPass {
					passCount++
				}
			}
			Expect(passCount).To(BeNumerically(">=", 2))
		})

		It("should fail with malformed YAML", func() {
			bad := writeFile("bad.yaml", `kpis:
  - id: test
    promquery: [invalid`)
			doctorFlags.PromKPIsConfig = bad

			checks := appendPromKPIChecks(nil)

			Expect(checks[0].status).To(Equal(statusFail))
			Expect(checks[0].message).To(ContainSubstring("failed to load"))
		})

		It("should fail with invalid PromQL", func() {
			bad := writeFile("badpromql.yaml", `kpis:
  - id: broken
    promquery: "rate(metric[5m]"
`)
			doctorFlags.PromKPIsConfig = bad
			doctorFlags.SamplingFreq = 60 * time.Second
			doctorFlags.Duration = 45 * time.Minute

			checks := appendPromKPIChecks(nil)

			failCount := 0
			for _, c := range checks {
				if c.status == statusFail {
					failCount++
				}
			}
			Expect(failCount).To(BeNumerically(">=", 1))
		})

		It("should fail with duplicate IDs", func() {
			dup := writeFile("dup.yaml", `kpis:
  - id: same
    promquery: up
  - id: same
    promquery: down
`)
			doctorFlags.PromKPIsConfig = dup
			doctorFlags.SamplingFreq = 60 * time.Second
			doctorFlags.Duration = 45 * time.Minute

			checks := appendPromKPIChecks(nil)

			failCount := 0
			for _, c := range checks {
				if c.status == statusFail {
					failCount++
				}
			}
			Expect(failCount).To(BeNumerically(">=", 1))
		})
	})

	Describe("appendKPIWarnings", func() {
		It("should warn when many KPIs have no category", func() {
			queries := make([]config.Query, 16)
			for i := range queries {
				queries[i] = config.Query{ID: "q", PromQuery: "up"}
			}
			kpis := config.KPIs{Queries: queries}
			doctorFlags.SamplingFreq = 60 * time.Second
			doctorFlags.Duration = 45 * time.Minute

			checks := appendKPIWarnings(nil, kpis)

			warnCount := 0
			for _, c := range checks {
				if c.status == statusWarn {
					warnCount++
				}
			}
			Expect(warnCount).To(BeNumerically(">=", 1))
		})

		It("should warn when frequency exceeds duration", func() {
			kpis := config.KPIs{
				Queries: []config.Query{
					{ID: "slow", PromQuery: "up"},
				},
			}
			doctorFlags.SamplingFreq = 2 * time.Hour
			doctorFlags.Duration = 30 * time.Minute

			checks := appendKPIWarnings(nil, kpis)

			warnFound := false
			for _, c := range checks {
				if c.status == statusWarn && c.message != "" {
					warnFound = true
				}
			}
			Expect(warnFound).To(BeTrue())
		})
	})

	Describe("appendTasksSpecChecks", func() {
		It("should validate a tasks file with all task types", func() {
			kpisFile := writeFile("kpis.yaml", validKPIsYAML)
			tasksYAML := fmt.Sprintf(`orchestration:
  mode: sequential
  on-failure: continue
prometheus:
  configFile: %s
per-node-data:
  duration: 30s
  interval: 5s
  image: registry.access.redhat.com/ubi9/ubi-minimal:latest
  workloadNamespaces: [default]
oslat:
  timeout: 1m
  pod:
    metadata:
      name: oslat-pod
    spec:
      containers:
        - name: oslat
          image: registry.access.redhat.com/ubi9/ubi-minimal:latest
app-recovery-time:
  duration: 30s
  interval: 5s
  image: registry.access.redhat.com/ubi9/ubi-minimal:latest
  workloadNamespaces: [default]
  nodeNames: [worker-0]
`, kpisFile)
			tasksPath := writeFile("tasks.yaml", tasksYAML)
			doctorFlags.TasksConfig = tasksPath
			doctorFlags.SamplingFreq = 60 * time.Second
			doctorFlags.Duration = 45 * time.Minute

			checks := appendTasksSpecChecks(nil)

			passCount := 0
			for _, c := range checks {
				if c.status == statusPass {
					passCount++
				}
			}
			// Expect passes for: spec loaded, orchestration, prometheus KPIs loaded,
			// prometheus validation, per-node-data, oslat, app-recovery-time
			Expect(passCount).To(BeNumerically(">=", 7))

			// Verify each task type is mentioned
			allMessages := ""
			for _, c := range checks {
				allMessages += c.message + "\n"
			}
			Expect(allMessages).To(ContainSubstring("prometheus"))
			Expect(allMessages).To(ContainSubstring("per-node-data"))
			Expect(allMessages).To(ContainSubstring("oslat"))
			Expect(allMessages).To(ContainSubstring("app-recovery-time"))
		})

		It("should fail with an invalid tasks file", func() {
			bad := writeFile("tasks.yaml", `prometheus: {}
`)
			doctorFlags.TasksConfig = bad

			checks := appendTasksSpecChecks(nil)

			Expect(checks[0].status).To(Equal(statusFail))
			Expect(checks[0].message).To(ContainSubstring("failed to load"))
		})
	})

	Describe("appendDatabaseChecks", func() {
		It("should pass for sqlite with existing db", func() {
			dbPath := filepath.Join(tmpDir, database.DefaultDBFileName)
			Expect(os.WriteFile(dbPath, []byte{}, 0644)).To(Succeed())
			doctorFlags.DatabaseType = "sqlite"

			checks := appendDatabaseChecks(nil)

			passCount := 0
			for _, c := range checks {
				if c.status == statusPass {
					passCount++
				}
			}
			Expect(passCount).To(Equal(2))
		})

		It("should warn for sqlite when db does not exist yet", func() {
			doctorFlags.DatabaseType = "sqlite"

			checks := appendDatabaseChecks(nil)

			Expect(checks).To(HaveLen(2))
			Expect(checks[0].status).To(Equal(statusPass))
			Expect(checks[1].status).To(Equal(statusWarn))
			Expect(checks[1].message).To(ContainSubstring("does not exist yet"))
		})

		It("should fail for postgres without URL", func() {
			doctorFlags.DatabaseType = "postgres"
			doctorFlags.PostgresURL = ""

			checks := appendDatabaseChecks(nil)

			failFound := false
			for _, c := range checks {
				if c.status == statusFail {
					failFound = true
				}
			}
			Expect(failFound).To(BeTrue())
		})

		It("should pass for postgres with URL", func() {
			doctorFlags.DatabaseType = "postgres"
			doctorFlags.PostgresURL = "postgresql://user:pass@localhost:5432/kpi"

			checks := appendDatabaseChecks(nil)

			passCount := 0
			for _, c := range checks {
				if c.status == statusPass {
					passCount++
				}
			}
			Expect(passCount).To(Equal(2))
		})

		It("should fail with invalid database type", func() {
			doctorFlags.DatabaseType = "mysql"

			checks := appendDatabaseChecks(nil)

			Expect(checks[0].status).To(Equal(statusFail))
			Expect(checks[0].message).To(ContainSubstring("invalid db-type"))
		})
	})

	Describe("appendArtifactsCheck", func() {
		It("should pass when directory exists", func() {
			checks := appendArtifactsCheck(nil)

			Expect(checks).To(HaveLen(1))
			Expect(checks[0].status).To(Equal(statusPass))
		})

		It("should warn when directory does not exist", func() {
			database.OutputDir = filepath.Join(tmpDir, "nonexistent")

			checks := appendArtifactsCheck(nil)

			Expect(checks).To(HaveLen(1))
			Expect(checks[0].status).To(Equal(statusWarn))
		})
	})

	Describe("printReport", func() {
		It("should return nil when there are no errors", func() {
			checks := []check{
				{statusPass, "ok"},
				{statusWarn, "advisory"},
			}

			err := printReport(checks)

			Expect(err).NotTo(HaveOccurred())
		})

		It("should return an error when there are failures", func() {
			checks := []check{
				{statusPass, "ok"},
				{statusFail, "broken"},
			}

			err := printReport(checks)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("1 configuration error"))
		})
	})

	Describe("runDoctor end-to-end", func() {
		It("should succeed with a fully valid configuration", func() {
			kpis := writeFile("kpis.yaml", validKPIsYAML)
			setValidFlags(kpis)

			err := runDoctor(nil, nil)

			Expect(err).NotTo(HaveOccurred())
		})

		It("should succeed when only config file is provided (no cluster/auth flags)", func() {
			kpis := writeFile("kpis.yaml", validKPIsYAML)
			doctorFlags = config.InputFlags{
				SamplingFreq:   60 * time.Second,
				Duration:       45 * time.Minute,
				DatabaseType:   "sqlite",
				PromKPIsConfig: kpis,
			}

			err := runDoctor(nil, nil)

			Expect(err).NotTo(HaveOccurred())
		})

		It("should fail when KPI file does not exist", func() {
			setValidFlags("/nonexistent/kpis.yaml")

			err := runDoctor(nil, nil)

			Expect(err).To(HaveOccurred())
		})
	})
})
