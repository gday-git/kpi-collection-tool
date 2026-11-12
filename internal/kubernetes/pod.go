package kubernetes

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	podPollInterval  = 5 * time.Second
	defaultNamespace = "default"
	podDeleteWait    = 2 * time.Minute
)

// CreatePod creates the pod. Empty namespace becomes "default".
func CreatePod(ctx context.Context, client kubernetes.Interface, pod *corev1.Pod) (*corev1.Pod, error) {
	if pod.Namespace == "" {
		pod.Namespace = defaultNamespace
	}
	created, err := client.CoreV1().Pods(pod.Namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create pod %s/%s: %w", pod.Namespace, pod.Name, err)
	}
	return created, nil
}

// DeletePod force-deletes the pod (grace period 0 — the tool only deletes its
// own pods) and waits until the object is gone from the API, so a pod with
// the same name can be recreated immediately. Without this, deleting a
// running pod leaves it in Terminating for its grace period and recreating it
// fails with "object is being deleted". NotFound is not an error. Cancelling
// ctx does not cancel the delete, so a task can still remove its pods when
// the run is interrupted.
func DeletePod(ctx context.Context, client kubernetes.Interface, namespace, name string) error {
	if namespace == "" {
		namespace = defaultNamespace
	}
	grace := int64(0)
	err := client.CoreV1().Pods(namespace).Delete(context.WithoutCancel(ctx), name, metav1.DeleteOptions{
		GracePeriodSeconds: &grace,
	})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to delete pod %s/%s: %w", namespace, name, err)
	}
	return waitForPodDeleted(ctx, client, namespace, name)
}

// CreateOwnedSecret creates a secret named baseName — or the next free
// baseName-<n> variant if that name is taken by a secret the tool doesn't own —
// and returns the name used, so callers can reference it (the pod's secret
// volume source). A secret already carrying the managed-by label is updated in
// place (a leftover from a prior run); updates go through the fetched object so
// its ResourceVersion is preserved. A foreign secret is never read beyond its
// labels, never written, and never deleted. The labels param (may be nil) is
// stamped on the object in both the create and update paths.
func CreateOwnedSecret(ctx context.Context, client kubernetes.Interface, namespace, baseName string, data map[string][]byte, labels map[string]string) (string, error) {
	name := baseName
	// ponytail: linear probe capped at 100; switch to a run-id suffix if a
	// cluster ever legitimately holds 100 foreign secrets on our base name
	for attempt := 0; attempt < 100; attempt++ {
		existing, err := client.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(err):
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
				Data:       data,
			}
			if _, err := client.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
				return "", fmt.Errorf("failed to create secret %s/%s: %w", namespace, name, err)
			}
			return name, nil
		case err != nil:
			return "", fmt.Errorf("failed to get secret %s/%s: %w", namespace, name, err)
		case existing.Labels[ManagedByLabel] == ManagedByValue:
			// Ours (e.g. a leftover that survived the pre-run sweep): update in place.
			existing.Data = data
			if existing.Labels == nil {
				existing.Labels = map[string]string{}
			}
			for k, v := range labels {
				existing.Labels[k] = v
			}
			if _, err := client.CoreV1().Secrets(namespace).Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
				return "", fmt.Errorf("failed to update secret %s/%s: %w", namespace, name, err)
			}
			return name, nil
		default:
			// Foreign secret under this name: never touch it, probe the next name.
			name = fmt.Sprintf("%s-%d", baseName, attempt+2)
		}
	}
	return "", fmt.Errorf("no free secret name derived from %q in namespace %s after 100 attempts", baseName, namespace)
}

// waitForPodDeleted polls until the pod is gone from the API, bounded by
// podDeleteWait so a stuck deletion (e.g. a finalizer) surfaces as an error
// instead of blocking forever.
func waitForPodDeleted(ctx context.Context, client kubernetes.Interface, namespace, name string) error {
	waitCtx, cancel := context.WithTimeout(ctx, podDeleteWait)
	defer cancel()

	ticker := time.NewTicker(podPollInterval)
	defer ticker.Stop()

	for {
		_, err := client.CoreV1().Pods(namespace).Get(waitCtx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("failed to get pod %s/%s after delete: %w", namespace, name, err)
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("timed out waiting for pod %s/%s to be deleted: %w", namespace, name, waitCtx.Err())
		case <-ticker.C:
		}
	}
}

// DeleteSecret deletes the secret if it exists; NotFound is not an error.
func DeleteSecret(ctx context.Context, client kubernetes.Interface, namespace, name string) error {
	err := client.CoreV1().Secrets(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to delete secret %s/%s: %w", namespace, name, err)
	}
	return nil
}

// PodWaitTick is called on each poll while waiting for a pod to finish.
type PodWaitTick func(phase string, elapsed time.Duration)

// PodWaitParams configures WaitForPodTerminal. OnTick is optional.
type PodWaitParams struct {
	Ctx       context.Context
	Client    kubernetes.Interface
	Namespace string
	Name      string
	Timeout   time.Duration
	OnTick    PodWaitTick
}

// WaitForPodTerminal polls until Succeeded or Failed, or until timeout / cancel.
// When OnTick is set, it is called after each successful Get.
func WaitForPodTerminal(podWaitParams PodWaitParams) (*corev1.Pod, error) {
	waitCtx, cancel := context.WithTimeout(podWaitParams.Ctx, podWaitParams.Timeout)
	defer cancel()

	ticker := time.NewTicker(podPollInterval)
	defer ticker.Stop()
	start := time.Now()

	for {
		pod, err := podWaitParams.Client.CoreV1().Pods(podWaitParams.Namespace).Get(waitCtx, podWaitParams.Name, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to get pod %s/%s: %w", podWaitParams.Namespace, podWaitParams.Name, err)
		}
		if podWaitParams.OnTick != nil {
			podWaitParams.OnTick(string(pod.Status.Phase), time.Since(start))
		}
		switch pod.Status.Phase {
		case corev1.PodSucceeded:
			return pod, nil
		case corev1.PodFailed:
			return pod, fmt.Errorf("pod %s/%s failed", podWaitParams.Namespace, podWaitParams.Name)
		}
		select {
		case <-waitCtx.Done():
			return pod, fmt.Errorf("timed out waiting for pod %s/%s: %w", podWaitParams.Namespace, podWaitParams.Name, waitCtx.Err())
		case <-ticker.C:
		}
	}
}

// GetPodLogs concatenates logs from pod.Spec.Containers.
func GetPodLogs(ctx context.Context, client kubernetes.Interface, pod *corev1.Pod) (string, error) {
	var b strings.Builder
	multi := len(pod.Spec.Containers) > 1
	for _, c := range pod.Spec.Containers {
		req := client.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: c.Name})
		rc, err := req.Stream(ctx)
		if err != nil {
			return b.String(), fmt.Errorf("failed to get logs for container %q: %w", c.Name, err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return b.String(), err
		}
		if multi {
			fmt.Fprintf(&b, "=== container %s ===\n", c.Name)
		}
		b.Write(data)
		if len(data) > 0 && data[len(data)-1] != '\n' {
			b.WriteByte('\n')
		}
	}
	return b.String(), nil
}
