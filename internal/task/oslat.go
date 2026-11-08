package task

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/config"
	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/kubernetes"
	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/output"
)

const oslatLogsFileName = "oslat_logs.out"

// OslatTask creates a user-supplied Pod, waits for it to finish, and writes logs.
type OslatTask struct {
	cfg          config.OslatTaskConfig
	kubeconfig   string
	artifactsDir string
	cleanup      bool
}

// NewOslatTask constructs an oslat task.
func NewOslatTask(cfg config.OslatTaskConfig, kubeconfig, artifactsDir string) *OslatTask {
	return &OslatTask{cfg: cfg, kubeconfig: kubeconfig, artifactsDir: artifactsDir}
}

func (t *OslatTask) Name() string { return config.TaskConfigOslat }

func (t *OslatTask) Run(ctx context.Context) error {
	client, err := kubernetes.ClientsetFromKubeconfig(t.kubeconfig)
	if err != nil {
		return fmt.Errorf("%s: %w", t.Name(), err)
	}

	pod := t.cfg.Pod.DeepCopy()
	if pod.APIVersion == "" {
		pod.APIVersion = "v1"
	}
	if pod.Kind == "" {
		pod.Kind = "Pod"
	}

	created, err := kubernetes.CreatePod(ctx, client, pod)
	if err != nil {
		return fmt.Errorf("%s: %w", t.Name(), err)
	}
	if t.cleanup {
		defer removePod(ctx, client, created.Namespace, created.Name, t.Name())
	}

	ns := created.Namespace
	output.PrintTaskProgress(t.Name(), fmt.Sprintf("waiting for pod %s/%s (timeout=%s)", ns, created.Name, t.cfg.Timeout))
	waitReporter := output.NewWaitReporter(t.Name())
	finished, waitErr := kubernetes.WaitForPodTerminal(kubernetes.PodWaitParams{
		Ctx:       ctx,
		Client:    client,
		Namespace: created.Namespace,
		Name:      created.Name,
		Timeout:   t.cfg.Timeout.Duration,
		OnTick:    waitReporter.OnPhase,
	})
	logPod := created
	if finished != nil {
		logPod = finished
	}

	body, logErr := kubernetes.GetPodLogs(ctx, client, logPod)
	path := filepath.Join(t.artifactsDir, oslatLogsFileName)
	if writeErr := os.WriteFile(path, []byte(body), 0600); writeErr != nil {
		return fmt.Errorf("%s: failed to write %s: %w", t.Name(), path, writeErr)
	}
	if waitErr != nil {
		return fmt.Errorf("%s: %w", t.Name(), waitErr)
	}
	if logErr != nil {
		return fmt.Errorf("%s: %w", t.Name(), logErr)
	}
	return nil
}
