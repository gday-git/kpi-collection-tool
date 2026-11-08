package task

import (
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/config"
)

func validOslatConfig() *config.OslatTaskConfig {
	return &config.OslatTaskConfig{
		Timeout: config.Duration{Duration: 12*time.Hour + 30*time.Minute},
		Pod: &config.Pod{Pod: corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "oslat"},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name:  "oslat",
					Image: "example.registry/oslat:latest",
				}},
			},
		}},
	}
}

func validPerNodeConfig() *config.PerNodeDataTaskConfig {
	return &config.PerNodeDataTaskConfig{
		WorkloadNamespaces: []string{"workload"},
		Duration:           config.Duration{Duration: 30 * time.Minute},
		Interval:           config.Duration{Duration: 5 * time.Second},
		Image:              "example.registry/debug:latest",
	}
}

func validRecoveryConfig() *config.AppRecoveryTimeTaskConfig {
	return &config.AppRecoveryTimeTaskConfig{
		WorkloadNamespaces: []string{"workload"},
		NodeNames:          []string{"worker-1"},
		Duration:           config.Duration{Duration: 30 * time.Minute},
		Interval:           config.Duration{Duration: time.Minute},
		Image:              "example.registry/ubi-minimal:latest",
	}
}

var _ = Describe("PromKPITask", func() {
	It("uses the prometheus task-config name", func() {
		t := NewPromKPITask(config.KPIs{}, config.InputFlags{})
		Expect(t.Name()).To(Equal(config.TaskConfigPrometheus))
	})
})

var _ = Describe("FromPromKPIsFlag", func() {
	It("returns one prometheus task when --prom-kpis-config is set", func() {
		flags := config.InputFlags{PromKPIsConfig: "kpis.yaml"}
		kpis := config.KPIs{Queries: []config.Query{{ID: "cpu", PromQuery: "up"}}}

		tasks, err := FromPromKPIsFlag(flags, kpis)
		Expect(err).NotTo(HaveOccurred())
		Expect(tasks).To(HaveLen(1))
		Expect(tasks[0].Name()).To(Equal(config.TaskConfigPrometheus))
	})

	It("returns an error when --prom-kpis-config is not set", func() {
		flags := config.InputFlags{}
		kpis := config.KPIs{}

		tasks, err := FromPromKPIsFlag(flags, kpis)
		Expect(err).To(HaveOccurred())
		Expect(tasks).To(BeNil())
		Expect(err.Error()).To(ContainSubstring("--prom-kpis-config is required"))
	})
})

var _ = Describe("unimplemented task stubs", func() {
	It("oslat is named", func() {
		t := NewOslatTask(config.OslatTaskConfig{}, "kubeconfig", "/tmp")
		Expect(t.Name()).To(Equal(config.TaskConfigOslat))
	})

	It("per-node-data is named", func() {
		t := NewPerNodeDataTask(config.PerNodeDataTaskConfig{}, "kubeconfig", "/tmp")
		Expect(t.Name()).To(Equal(config.TaskConfigPerNodeData))
	})

	It("app-recovery-time is named", func() {
		t := NewAppRecoveryTimeTask(config.AppRecoveryTimeTaskConfig{}, "kubeconfig", "/tmp")
		Expect(t.Name()).To(Equal(config.TaskConfigAppRecoveryTime))
	})
})

var _ = Describe("ResolveFromTasksSpec", func() {
	var tmpDir string

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "run-config-resolve-*")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		Expect(os.RemoveAll(tmpDir)).To(Succeed())
	})

	flags := config.InputFlags{ClusterName: "test-cluster"}

	It("passes orchestration.cleanup to pod-creating tasks", func() {
		cfg := config.TasksSpec{
			Orchestration: config.OrchestrationConfig{Cleanup: true},
			Oslat:         validOslatConfig(),
			PerNodeData:   validPerNodeConfig(),
		}
		withKubeconfig := config.InputFlags{ClusterName: "test-cluster", Kubeconfig: "kubeconfig"}

		tasks, err := ResolveFromTasksSpec(cfg, withKubeconfig)
		Expect(err).NotTo(HaveOccurred())
		Expect(tasks).To(HaveLen(2))
		Expect(tasks[0].(*PerNodeDataTask).cleanup).To(BeTrue())
		Expect(tasks[1].(*OslatTask).cleanup).To(BeTrue())
	})

	It("builds a prometheus task from inline kpis", func() {
		cfg := config.TasksSpec{
			Prometheus: &config.PrometheusTaskConfig{
				Kpis: []config.Query{{ID: "node-cpu", PromQuery: "up"}},
			},
		}

		tasks, err := ResolveFromTasksSpec(cfg, flags)
		Expect(err).NotTo(HaveOccurred())
		Expect(tasks).To(HaveLen(1))
		Expect(tasks[0].Name()).To(Equal(config.TaskConfigPrometheus))
	})

	It("builds a prometheus task from a configFile", func() {
		kpisPath := filepath.Join(tmpDir, "prom-kpis.yaml")
		Expect(os.WriteFile(kpisPath, []byte(`
kpis:
  - id: node-cpu
    promquery: up
`), 0644)).To(Succeed())

		cfg := config.TasksSpec{
			Prometheus: &config.PrometheusTaskConfig{ConfigFile: kpisPath},
		}

		tasks, err := ResolveFromTasksSpec(cfg, flags)
		Expect(err).NotTo(HaveOccurred())
		Expect(tasks).To(HaveLen(1))
		Expect(tasks[0].Name()).To(Equal(config.TaskConfigPrometheus))
	})

	It("resolves a relative configFile against the tasks file directory", func() {
		Expect(os.WriteFile(filepath.Join(tmpDir, "prom-kpis.yaml"), []byte(`
kpis:
  - id: node-cpu
    promquery: up
`), 0644)).To(Succeed())
		tasksPath := filepath.Join(tmpDir, "tasks.yaml")
		Expect(os.WriteFile(tasksPath, []byte(`
prometheus:
  configFile: prom-kpis.yaml
`), 0644)).To(Succeed())

		cfg, err := config.LoadTasksSpec(tasksPath)
		Expect(err).NotTo(HaveOccurred())

		tasks, err := ResolveFromTasksSpec(cfg, flags)
		Expect(err).NotTo(HaveOccurred())
		Expect(tasks).To(HaveLen(1))
		Expect(tasks[0].Name()).To(Equal(config.TaskConfigPrometheus))
	})

	It("requires kubeconfig for oslat", func() {
		cfg := config.TasksSpec{Oslat: validOslatConfig()}
		tasks, err := ResolveFromTasksSpec(cfg, flags)
		Expect(err).To(HaveOccurred())
		Expect(tasks).To(BeNil())
		Expect(err.Error()).To(ContainSubstring(config.TaskConfigOslat))
		Expect(err.Error()).To(ContainSubstring("--kubeconfig is required"))
	})

	It("requires kubeconfig for per-node-data", func() {
		cfg := config.TasksSpec{PerNodeData: validPerNodeConfig()}
		tasks, err := ResolveFromTasksSpec(cfg, flags)
		Expect(err).To(HaveOccurred())
		Expect(tasks).To(BeNil())
		Expect(err.Error()).To(ContainSubstring(config.TaskConfigPerNodeData))
		Expect(err.Error()).To(ContainSubstring("--kubeconfig is required"))
	})

	It("requires kubeconfig for app-recovery-time", func() {
		cfg := config.TasksSpec{
			AppRecoveryTime: validRecoveryConfig(),
		}

		tasks, err := ResolveFromTasksSpec(cfg, flags)
		Expect(err).To(HaveOccurred())
		Expect(tasks).To(BeNil())
		Expect(err.Error()).To(ContainSubstring(config.TaskConfigAppRecoveryTime))
		Expect(err.Error()).To(ContainSubstring("--kubeconfig is required"))
	})

	It("uses default order so oslat is resolved before later tasks", func() {
		cfg := config.TasksSpec{
			Oslat:           validOslatConfig(),
			AppRecoveryTime: validRecoveryConfig(),
		}
		_, err := ResolveFromTasksSpec(cfg, flags)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(config.TaskConfigOslat))
		Expect(err.Error()).To(ContainSubstring("--kubeconfig is required"))
	})

	It("honors orchestration.order when choosing which task fails first without kubeconfig", func() {
		cfg := config.TasksSpec{
			Orchestration: config.OrchestrationConfig{
				Order: []string{config.TaskConfigAppRecoveryTime, config.TaskConfigOslat},
			},
			Oslat:           validOslatConfig(),
			AppRecoveryTime: validRecoveryConfig(),
		}

		_, err := ResolveFromTasksSpec(cfg, flags)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(config.TaskConfigAppRecoveryTime))
	})

	It("returns an error when no task configs are present", func() {
		tasks, err := ResolveFromTasksSpec(config.TasksSpec{}, flags)
		Expect(err).To(HaveOccurred())
		Expect(tasks).To(BeNil())
		Expect(err.Error()).To(ContainSubstring("no task configs defined"))
	})
})
