package task

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8s "k8s.io/client-go/kubernetes"

	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/config"
	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/kubernetes"
	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/output"
)

const (
	meminfoMarker      = "___KPI_MEMINFO___"
	cmdlineMarker      = "___KPI_CMDLINE___"
	hwInventoryMarker  = "___KPI_HW_INVENTORY___"
	debugWaitBuffer    = 10 * time.Minute
)

// collectSection describes one marker-delimited section the collect script
// emits after `top`. It drives env-var plumbing, log splitting, and artifact
// file naming.
type collectSection struct {
	envVar string // env var the script reads the marker from
	marker string
	prefix string // artifact file is prefix + node + suffix
	suffix string
}

var baseCollectSections = []collectSection{
	{"MEMINFO_MARKER", meminfoMarker, "proc_meminfo-", ".txt"},
	{"CMDLINE_MARKER", cmdlineMarker, "proc_cmdline-", ".txt"},
}

//go:embed per_node_collect.sh
var collectScript string

// PerNodeDataTask runs a debug pod on every node, then writes API YAML.
type PerNodeDataTask struct {
	cfg          config.PerNodeDataTaskConfig
	kubeconfig   string
	proxyURL     string
	artifactsDir string
}

// NewPerNodeDataTask constructs a per-node-data task.
func NewPerNodeDataTask(cfg config.PerNodeDataTaskConfig, kubeconfig, proxyURL, artifactsDir string) *PerNodeDataTask {
	return &PerNodeDataTask{cfg: cfg, kubeconfig: kubeconfig, proxyURL: proxyURL, artifactsDir: artifactsDir}
}

func (t *PerNodeDataTask) Name() string { return config.TaskConfigPerNodeData }

// sections returns the marker-delimited sections the collect script will
// emit for this task: the base sections, plus hardware inventory when
// enhanced-collection is enabled.
func (t *PerNodeDataTask) sections() []collectSection {
	if !t.cfg.EnhancedCollection {
		return baseCollectSections
	}
	return append(append([]collectSection{}, baseCollectSections...),
		collectSection{envVar: "HW_INVENTORY_MARKER", marker: hwInventoryMarker, prefix: "hw_inventory-", suffix: ".txt"})
}

func (t *PerNodeDataTask) Run(ctx context.Context) error {
	if t.cfg.Isolcpus == "" {
		return fmt.Errorf("%s: isolcpus is empty", t.Name())
	}
	log.Printf("%s: using isolcpus=%s", t.Name(), t.cfg.Isolcpus)

	client, err := kubernetes.ClientsetFromKubeconfig(t.kubeconfig, t.proxyURL)
	if err != nil {
		return fmt.Errorf("%s: %w", t.Name(), err)
	}

	nodes, err := kubernetes.ListNodeNames(ctx, client)
	if err != nil {
		return fmt.Errorf("%s: %w", t.Name(), err)
	}

	// A killed prior run leaves its debug pods behind (defers do not survive
	// SIGKILL); sweep them so this run does not collide with stale pods.
	deleted, err := kubernetes.DeletePodsByPrefix(ctx, client, "default", "pnd-")
	if err != nil {
		log.Printf("%s: warning: failed to remove leftover debug pods: %v", t.Name(), err)
	} else if len(deleted) > 0 {
		log.Printf("%s: removed leftover debug pods from a previous run: %v", t.Name(), deleted)
	}

	output.PrintTaskProgress(t.Name(), fmt.Sprintf(
		"collecting from %d node(s) (duration=%s)", len(nodes), t.cfg.Duration))

	nodeErr := t.collectAll(ctx, client, nodes)
	descErr := t.writeDescribes(ctx, client)
	if nodeErr != nil {
		if descErr != nil {
			return fmt.Errorf("%s: %w; describe: %v", t.Name(), nodeErr, descErr)
		}
		return fmt.Errorf("%s: %w", t.Name(), nodeErr)
	}
	if descErr != nil {
		return fmt.Errorf("%s: %w", t.Name(), descErr)
	}
	return nil
}

func (t *PerNodeDataTask) collectAll(ctx context.Context, client *k8s.Clientset, nodes []string) error {
	var mu sync.Mutex
	var errs []error
	var wg sync.WaitGroup
	for _, node := range nodes {
		wg.Go(func() {
			if err := t.collectNode(ctx, client, node); err != nil {
				log.Printf("%s: %v", t.Name(), err)
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("failed on %d node(s): %v", len(errs), errs)
}

func (t *PerNodeDataTask) collectNode(ctx context.Context, client *k8s.Clientset, node string) error {
	output.PrintTaskProgress(t.Name(), node+": collecting")
	created, err := kubernetes.CreatePod(ctx, client, t.debugPod(node))
	if err != nil {
		return fmt.Errorf("%s: %w", node, err)
	}

	// Delete the debug pod as soon as this node's logs and artifacts are
	// written — success or failure — so nothing outlives the task.
	defer func() {
		if err := kubernetes.DeletePod(context.WithoutCancel(ctx), client, created.Namespace, created.Name); err != nil {
			log.Printf("%s: %v", t.Name(), err)
		}
	}()

	wait := t.cfg.Duration.Duration + debugWaitBuffer
	finished, waitErr := kubernetes.WaitForPodTerminal(kubernetes.PodWaitParams{
		Ctx:       ctx,
		Client:    client,
		Namespace: created.Namespace,
		Name:      created.Name,
		Timeout:   wait,
	})
	logPod := created
	if finished != nil {
		logPod = finished
	}

	body, logErr := kubernetes.GetPodLogs(ctx, client, logPod)
	if writeErr := t.writeNodeArtifacts(node, body); writeErr != nil && logErr == nil {
		logErr = writeErr
	}
	if waitErr != nil {
		return fmt.Errorf("%s: %w", node, waitErr)
	}
	if logErr != nil {
		return fmt.Errorf("%s: %w", node, logErr)
	}
	return nil
}

func (t *PerNodeDataTask) debugPod(node string) *corev1.Pod {
	privileged := true
	n := int(t.cfg.Duration.Duration / t.cfg.Interval.Duration)
	env := []corev1.EnvVar{
		{Name: "ISOLCPUS", Value: t.cfg.Isolcpus},
		{Name: "TOP_DELAY", Value: fmt.Sprintf("%g", t.cfg.Interval.Seconds())},
		{Name: "TOP_COUNT", Value: strconv.Itoa(n)},
	}
	for _, s := range t.sections() {
		env = append(env, corev1.EnvVar{Name: s.envVar, Value: s.marker})
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: debugPodName(node)},
		Spec: corev1.PodSpec{
			NodeName:      node,
			RestartPolicy: corev1.RestartPolicyNever,
			Tolerations:   []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
			Containers: []corev1.Container{{
				Name:            "collect",
				Image:           t.cfg.Image,
				ImagePullPolicy: corev1.PullIfNotPresent,
				Command:         []string{"chroot", "/host", "/bin/bash", "-c", collectScript},
				Env:             env,
				SecurityContext: &corev1.SecurityContext{Privileged: &privileged},
				VolumeMounts:    []corev1.VolumeMount{{Name: "host", MountPath: "/host"}},
			}},
			Volumes: []corev1.Volume{{
				Name:         "host",
				VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/"}},
			}},
		},
	}
}

func (t *PerNodeDataTask) writeNodeArtifacts(node, body string) error {
	parts, err := splitCollectLogs(body, t.sections())
	if err != nil {
		_ = os.WriteFile(filepath.Join(t.artifactsDir, "top-"+node+".out"), []byte(body), 0600)
		return err
	}
	files := map[string]string{"top-" + node + ".out": parts[0]}
	for i, s := range t.sections() {
		files[s.prefix+node+s.suffix] = parts[i+1]
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(t.artifactsDir, name), []byte(content), 0600); err != nil {
			return fmt.Errorf("failed to write %s: %w", name, err)
		}
	}
	return nil
}

func (t *PerNodeDataTask) writeDescribes(ctx context.Context, client *k8s.Clientset) error {
	for _, ns := range t.cfg.WorkloadNamespaces {
		path := filepath.Join(t.artifactsDir, "describe_pods_"+ns+".txt")
		if err := kubernetes.WritePodsYAML(ctx, client, ns, path); err != nil {
			return err
		}
	}
	return kubernetes.WriteNodesYAML(ctx, client, filepath.Join(t.artifactsDir, "describe_nodes.txt"))
}

// splitCollectLogs splits the pod log into len(sections)+1 parts: everything
// before the first marker (the `top` output), then one part per section, up
// to the next marker or EOF. Markers are searched only in the remaining
// suffix, so out-of-order markers surface as a missing-marker error.
func splitCollectLogs(body string, sections []collectSection) ([]string, error) {
	parts := make([]string, 0, len(sections)+1)
	for _, s := range sections {
		key := s.marker + "\n"
		i := strings.Index(body, key)
		if i < 0 {
			return nil, fmt.Errorf("pod logs missing marker %s", s.marker)
		}
		parts = append(parts, body[:i])
		body = body[i+len(key):]
	}
	return append(parts, body), nil
}

// debugPodName turns a Kubernetes node name into a DNS-1123 pod name.
// Node names may contain dots (e.g. ip-10-0-1-2.ec2.internal); pod names
// may only use lowercase [a-z0-9-] and are capped at 63 characters.
func debugPodName(node string) string {
	name := "pnd-" + strings.ToLower(strings.ReplaceAll(node, ".", "-"))
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}
	return name
}
