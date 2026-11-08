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
	meminfoMarker   = "___KPI_MEMINFO___"
	cmdlineMarker   = "___KPI_CMDLINE___"
	debugWaitBuffer = 10 * time.Minute
)

//go:embed per_node_collect.sh
var collectScript string

// PerNodeDataTask runs a debug pod on every node, then writes API YAML.
type PerNodeDataTask struct {
	cfg          config.PerNodeDataTaskConfig
	kubeconfig   string
	artifactsDir string
	cleanup      bool
}

// NewPerNodeDataTask constructs a per-node-data task.
func NewPerNodeDataTask(cfg config.PerNodeDataTaskConfig, kubeconfig, artifactsDir string) *PerNodeDataTask {
	return &PerNodeDataTask{cfg: cfg, kubeconfig: kubeconfig, artifactsDir: artifactsDir}
}

func (t *PerNodeDataTask) Name() string { return config.TaskConfigPerNodeData }

func (t *PerNodeDataTask) Run(ctx context.Context) error {
	if t.cfg.Isolcpus == "" {
		return fmt.Errorf("%s: isolcpus is empty", t.Name())
	}
	log.Printf("%s: using isolcpus=%s", t.Name(), t.cfg.Isolcpus)

	client, err := kubernetes.ClientsetFromKubeconfig(t.kubeconfig)
	if err != nil {
		return fmt.Errorf("%s: %w", t.Name(), err)
	}

	nodes, err := kubernetes.ListNodeNames(ctx, client)
	if err != nil {
		return fmt.Errorf("%s: %w", t.Name(), err)
	}
	output.PrintTaskProgress(t.Name(), fmt.Sprintf("collecting from %d node(s)", len(nodes)))

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
	if t.cleanup {
		defer removePod(ctx, client, created.Namespace, created.Name, t.Name())
	}

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
				Env: []corev1.EnvVar{
					{Name: "ISOLCPUS", Value: t.cfg.Isolcpus},
					{Name: "TOP_DELAY", Value: fmt.Sprintf("%g", t.cfg.Interval.Seconds())},
					{Name: "TOP_COUNT", Value: strconv.Itoa(n)},
					{Name: "MEMINFO_MARKER", Value: meminfoMarker},
					{Name: "CMDLINE_MARKER", Value: cmdlineMarker},
				},
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
	top, mem, cmdline, err := splitCollectLogs(body)
	if err != nil {
		_ = os.WriteFile(filepath.Join(t.artifactsDir, "top-"+node+".out"), []byte(body), 0600)
		return err
	}
	files := map[string]string{
		"top-" + node + ".out":          top,
		"proc_meminfo-" + node + ".txt": mem,
		"proc_cmdline-" + node + ".txt": cmdline,
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

func splitCollectLogs(body string) (top, mem, cmdline string, err error) {
	memKey := meminfoMarker + "\n"
	cmdKey := cmdlineMarker + "\n"
	i := strings.Index(body, memKey)
	j := strings.Index(body, cmdKey)
	if i < 0 || j < 0 || j < i {
		return "", "", "", fmt.Errorf("pod logs missing %s / %s markers", meminfoMarker, cmdlineMarker)
	}
	return body[:i], body[i+len(memKey) : j], body[j+len(cmdKey):], nil
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
