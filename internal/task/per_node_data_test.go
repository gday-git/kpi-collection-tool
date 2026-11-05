package task

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("debugPodName", func() {
	It("lowercases and replaces dots with hyphens", func() {
		Expect(debugPodName("Master-2.Clus0.example.com")).To(Equal("pnd-master-2-clus0-example-com"))
	})

	It("truncates to 63 characters and trims trailing hyphens", func() {
		longNode := strings.Repeat("a", 80) + ".b.c"
		name := debugPodName(longNode)
		Expect(len(name)).To(BeNumerically("<=", 63))
		Expect(name).To(HavePrefix("pnd-"))
		Expect(strings.HasSuffix(name, "-")).To(BeFalse())
	})
})

var _ = Describe("splitCollectLogs", func() {
	It("splits log sections on marker lines", func() {
		body := "top output\n" + meminfoMarker + "\nMemTotal: 1 kB\n" + cmdlineMarker + "\nBOOT_IMAGE=linux\n"

		parts, err := splitCollectLogs(body, baseCollectSections)
		Expect(err).NotTo(HaveOccurred())
		Expect(parts).To(Equal([]string{"top output\n", "MemTotal: 1 kB\n", "BOOT_IMAGE=linux\n"}))
	})

	It("splits an additional hardware inventory section when enabled", func() {
		body := "top\n" + meminfoMarker + "\nmem\n" + cmdlineMarker + "\ncmd\n" + hwInventoryMarker + "\nlscpu stuff\n"
		sections := append(append([]collectSection{}, baseCollectSections...),
			collectSection{envVar: "HW_INVENTORY_MARKER", marker: hwInventoryMarker, prefix: "hw_inventory-", suffix: ".txt"})

		parts, err := splitCollectLogs(body, sections)
		Expect(err).NotTo(HaveOccurred())
		Expect(parts).To(Equal([]string{"top\n", "mem\n", "cmd\n", "lscpu stuff\n"}))
	})

	It("errors when markers are missing", func() {
		_, err := splitCollectLogs("no markers here", baseCollectSections)
		Expect(err).To(MatchError(ContainSubstring("missing")))
	})

	It("errors when markers are out of order", func() {
		body := "top\n" + cmdlineMarker + "\ncmd\n" + meminfoMarker + "\nmem\n"
		_, err := splitCollectLogs(body, baseCollectSections)
		Expect(err).To(MatchError(ContainSubstring("missing")))
	})
})

var _ = Describe("PerNodeDataTask writeNodeArtifacts", func() {
	var tmpDir string

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "pnd-artifacts-*")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		Expect(os.RemoveAll(tmpDir)).To(Succeed())
	})

	It("writes split artifact files under the artifacts directory", func() {
		t := &PerNodeDataTask{artifactsDir: tmpDir}
		body := "top\n" + meminfoMarker + "\nmem\n" + cmdlineMarker + "\ncmd\n"

		err := t.writeNodeArtifacts("node-1", body)
		Expect(err).NotTo(HaveOccurred())

		top, err := os.ReadFile(filepath.Join(tmpDir, "top-node-1.out"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(top)).To(Equal("top\n"))

		mem, err := os.ReadFile(filepath.Join(tmpDir, "proc_meminfo-node-1.txt"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(mem)).To(Equal("mem\n"))

		cmdline, err := os.ReadFile(filepath.Join(tmpDir, "proc_cmdline-node-1.txt"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(cmdline)).To(Equal("cmd\n"))
	})

	It("writes a hardware inventory artifact when enhanced-collection is enabled", func() {
		t := &PerNodeDataTask{artifactsDir: tmpDir, cfg: config.PerNodeDataTaskConfig{EnhancedCollection: true}}
		body := "top\n" + meminfoMarker + "\nmem\n" + cmdlineMarker + "\ncmd\n" + hwInventoryMarker + "\nlscpu\n"

		err := t.writeNodeArtifacts("node-1", body)
		Expect(err).NotTo(HaveOccurred())

		hw, err := os.ReadFile(filepath.Join(tmpDir, "hw_inventory-node-1.txt"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(hw)).To(Equal("lscpu\n"))
	})

	It("does not write a hardware inventory artifact when enhanced-collection is disabled", func() {
		t := &PerNodeDataTask{artifactsDir: tmpDir}
		body := "top\n" + meminfoMarker + "\nmem\n" + cmdlineMarker + "\ncmd\n"

		err := t.writeNodeArtifacts("node-1", body)
		Expect(err).NotTo(HaveOccurred())

		_, err = os.Stat(filepath.Join(tmpDir, "hw_inventory-node-1.txt"))
		Expect(os.IsNotExist(err)).To(BeTrue())
	})

	It("falls back to a raw dump when markers are missing", func() {
		t := &PerNodeDataTask{artifactsDir: tmpDir}

		err := t.writeNodeArtifacts("node-1", "garbage")
		Expect(err).To(MatchError(ContainSubstring("missing")))

		top, err := os.ReadFile(filepath.Join(tmpDir, "top-node-1.out"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(top)).To(Equal("garbage"))
	})
})

var _ = Describe("PerNodeDataTask debugPod", func() {
	It("pins the pod to the node and sets collection env", func() {
		t := &PerNodeDataTask{
			cfg: config.PerNodeDataTaskConfig{
				Duration: config.Duration{Duration: 30 * time.Minute},
				Interval: config.Duration{Duration: 5 * time.Second},
				Image:    "debug:latest",
				Isolcpus: "2-3",
			},
		}

		pod := t.debugPod("worker-0.example.com")
		Expect(pod.Name).To(Equal(debugPodName("worker-0.example.com")))
		Expect(pod.Spec.NodeName).To(Equal("worker-0.example.com"))
		Expect(pod.Spec.Containers).To(HaveLen(1))
		Expect(pod.Spec.Containers[0].Image).To(Equal("debug:latest"))

		env := envMap(pod.Spec.Containers[0].Env)
		Expect(env["ISOLCPUS"]).To(Equal("2-3"))
		Expect(env["TOP_DELAY"]).To(Equal("5"))
		Expect(env["TOP_COUNT"]).To(Equal("360"))
		Expect(env["MEMINFO_MARKER"]).To(Equal(meminfoMarker))
		Expect(env["CMDLINE_MARKER"]).To(Equal(cmdlineMarker))
		Expect(env["HW_INVENTORY_MARKER"]).To(BeEmpty())
		Expect(*pod.Spec.Containers[0].SecurityContext.Privileged).To(BeTrue())
	})

	It("sets the hardware inventory marker env when enhanced-collection is enabled", func() {
		t := &PerNodeDataTask{
			cfg: config.PerNodeDataTaskConfig{
				Duration:           config.Duration{Duration: 30 * time.Minute},
				Interval:           config.Duration{Duration: 5 * time.Second},
				Image:              "debug:latest",
				Isolcpus:           "2-3",
				EnhancedCollection: true,
			},
		}

		pod := t.debugPod("worker-0.example.com")
		env := envMap(pod.Spec.Containers[0].Env)
		Expect(env["HW_INVENTORY_MARKER"]).To(Equal(hwInventoryMarker))
	})
})

func envMap(vars []corev1.EnvVar) map[string]string {
	m := make(map[string]string, len(vars))
	for _, v := range vars {
		m[v.Name] = v.Value
	}
	return m
}
