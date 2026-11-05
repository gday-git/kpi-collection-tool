package task

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	corev1 "k8s.io/api/core/v1"
	k8s "k8s.io/client-go/kubernetes"

	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/config"
	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/kubernetes"
	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/output"
)

const (
	oslatLogsFileName = "oslat_logs.out"
	// kubeconfigVolumeName is the volume injected when mountKubeconfig is set.
	kubeconfigVolumeName   = "kubeconfig"
	defaultKubeconfigMount = "/kubeconfig"
)

// OslatTask creates a user-supplied Pod, waits for it to finish, and writes logs.
type OslatTask struct {
	cfg          config.OslatTaskConfig
	kubeconfig   string
	proxyURL     string
	artifactsDir string
	cleanup      bool
}

// NewOslatTask constructs an oslat task.
func NewOslatTask(cfg config.OslatTaskConfig, kubeconfig, proxyURL, artifactsDir string) *OslatTask {
	return &OslatTask{cfg: cfg, kubeconfig: kubeconfig, proxyURL: proxyURL, artifactsDir: artifactsDir}
}

func (t *OslatTask) Name() string { return config.TaskConfigOslat }

func (t *OslatTask) Run(ctx context.Context) error {
	client, err := kubernetes.ClientsetFromKubeconfig(t.kubeconfig, t.proxyURL)
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

	if t.cfg.MountKubeconfig {
		if err := t.injectKubeconfig(ctx, client, pod); err != nil {
			return fmt.Errorf("%s: %w", t.Name(), err)
		}
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

// injectKubeconfig creates a Secret from the run's --kubeconfig and wires it into
// the pod: a secret-backed volume, a read-only mount, and a KUBECONFIG env var on
// every container. This replaces the manual `oc create secret` + hand-written
// volume boilerplate. The mounted kubeconfig must be proxy-free, since it runs
// in-cluster where the laptop proxy is unreachable (use --proxy-url instead).
func (t *OslatTask) injectKubeconfig(ctx context.Context, client k8s.Interface, pod *corev1.Pod) error {
	if t.kubeconfig == "" {
		return fmt.Errorf("mountKubeconfig is set but --kubeconfig was not provided")
	}
	data, err := os.ReadFile(t.kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to read kubeconfig %q: %w", t.kubeconfig, err)
	}

	ns := pod.Namespace
	if ns == "" {
		ns = "default"
		pod.Namespace = ns
	}
	secretName := pod.Name + "-kubeconfig"
	if err := kubernetes.CreateOrUpdateSecret(ctx, client, ns, secretName, map[string][]byte{"kubeconfig": data}); err != nil {
		return err
	}

	mountPath := t.cfg.MountPath
	if mountPath == "" {
		mountPath = defaultKubeconfigMount
	}

	pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
		Name: kubeconfigVolumeName,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{SecretName: secretName},
		},
	})
	for i := range pod.Spec.Containers {
		c := &pod.Spec.Containers[i]
		c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
			Name:      kubeconfigVolumeName,
			MountPath: mountPath,
			ReadOnly:  true,
		})
		if !hasEnv(c.Env, "KUBECONFIG") {
			c.Env = append(c.Env, corev1.EnvVar{Name: "KUBECONFIG", Value: mountPath + "/kubeconfig"})
		}
	}
	return nil
}

// hasEnv reports whether an env var with the given name is already set.
func hasEnv(env []corev1.EnvVar, name string) bool {
	for _, e := range env {
		if e.Name == name {
			return true
		}
	}
	return false
}
