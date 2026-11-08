package task

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8sclient "k8s.io/client-go/kubernetes"

	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/config"
	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/kubernetes"
	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/output"
)

const (
	podStatusFileName   = "pod_status.out"
	notReadyWaitTimeout = 5 * time.Minute
)

// AppRecoveryTimeTask reboots nodes, waits for NotReady, polls pod status, writes pod_status.out.
type AppRecoveryTimeTask struct {
	cfg          config.AppRecoveryTimeTaskConfig
	kubeconfig   string
	artifactsDir string
	cleanup      bool
}

// NewAppRecoveryTimeTask constructs an app-recovery-time task.
func NewAppRecoveryTimeTask(cfg config.AppRecoveryTimeTaskConfig, kubeconfig, artifactsDir string) *AppRecoveryTimeTask {
	return &AppRecoveryTimeTask{cfg: cfg, kubeconfig: kubeconfig, artifactsDir: artifactsDir}
}

func (t *AppRecoveryTimeTask) Name() string { return config.TaskConfigAppRecoveryTime }

// Run reboots nodeNames, waits until they are NotReady or the API is
// unreachable (SNO / control-plane reboot), then polls workload pods for
// duration. List/get failures are written to pod_status.out; the poll loop
// continues until duration elapses. File write errors and Ctrl+C still fail.
func (t *AppRecoveryTimeTask) Run(ctx context.Context) error {
	client, err := kubernetes.ClientsetFromKubeconfig(t.kubeconfig)
	if err != nil {
		return fmt.Errorf("%s: %w", t.Name(), err)
	}

	taskName := t.Name()
	for _, nodeName := range t.cfg.NodeNames {
		output.PrintTaskProgress(taskName, fmt.Sprintf("creating reboot pod on %s", nodeName))
		created, err := kubernetes.CreateRebootPod(ctx, client, nodeName, t.cfg.Image)
		if err != nil {
			return fmt.Errorf("%s: %w", t.Name(), err)
		}
		if t.cleanup {
			defer removePod(ctx, client, created.Namespace, created.Name, taskName)
		}
	}

	output.PrintTaskProgress(taskName, fmt.Sprintf("waiting for node(s) NotReady (timeout=%s)", notReadyWaitTimeout))
	onNotReady := func(node string) {
		output.PrintTaskProgress(taskName, fmt.Sprintf("%s is NotReady", node))
	}
	if err := kubernetes.WaitForNodesNotReady(ctx, client, t.cfg.NodeNames, notReadyWaitTimeout, onNotReady); err != nil {
		return fmt.Errorf("%s: %w", t.Name(), err)
	}

	output.PrintTaskProgress(taskName, fmt.Sprintf(
		"polling workload pods for %s (interval=%s)",
		t.cfg.Duration.Duration,
		t.cfg.Interval.Duration,
	))

	path := filepath.Join(t.artifactsDir, podStatusFileName)
	f, err := os.Create(path) //#nosec G304 -- path is tool-controlled artifacts dir
	if err != nil {
		return fmt.Errorf("%s: failed to create %s: %w", t.Name(), path, err)
	}
	defer func() { _ = f.Close() }()

	iterations := int(t.cfg.Duration.Duration / t.cfg.Interval.Duration)
	pollReporter := output.NewPollReporter(taskName)
	pollStart := time.Now()
	for i := 0; i < iterations; i++ {
		if err := t.writePollBlock(ctx, client, f, i > 0); err != nil {
			return fmt.Errorf("%s: %w", t.Name(), err)
		}
		pollReporter.MaybeHeartbeat(time.Since(pollStart))
		if i == iterations-1 {
			break
		}
		if err := sleepWithContext(ctx, t.cfg.Interval.Duration); err != nil {
			return fmt.Errorf("%s: %w", t.Name(), err)
		}
	}
	output.PrintTaskProgress(taskName, "wrote "+podStatusFileName)
	return nil
}

func (t *AppRecoveryTimeTask) writePollBlock(ctx context.Context, client k8sclient.Interface, w *os.File, leadingBlank bool) error {
	if leadingBlank {
		if _, err := w.WriteString("\n"); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "%s\n", time.Now().String()); err != nil {
		return err
	}
	if _, err := w.WriteString("NAMESPACE  NAME  PHASE  READY  READYTIME\n"); err != nil {
		return err
	}

	for _, ns := range t.cfg.WorkloadNamespaces {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		pods, err := kubernetes.ListPods(ctx, client, ns)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if _, werr := fmt.Fprintf(w, "error listing pods in %s: %v\n", ns, err); werr != nil {
				return werr
			}
			continue
		}
		for i := range pods {
			if err := writePodRow(w, &pods[i]); err != nil {
				return err
			}
		}
	}
	return nil
}

func writePodRow(w *os.File, pod *corev1.Pod) error {
	ready, readyTime := podReadyFields(pod)
	_, err := fmt.Fprintf(w, "%s  %s  %s  %s  %s\n",
		pod.Namespace, pod.Name, pod.Status.Phase, ready, readyTime)
	return err
}

func podReadyFields(pod *corev1.Pod) (status, lastTransition string) {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady {
			status = string(c.Status)
			if !c.LastTransitionTime.IsZero() {
				lastTransition = c.LastTransitionTime.UTC().Format(time.RFC3339)
			}
			return status, lastTransition
		}
	}
	return "", ""
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
