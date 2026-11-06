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

const podPollInterval = 5 * time.Second

// CreatePod creates the pod. Empty namespace becomes "default".
func CreatePod(ctx context.Context, client kubernetes.Interface, pod *corev1.Pod) (*corev1.Pod, error) {
	if pod.Namespace == "" {
		pod.Namespace = "default"
	}
	created, err := client.CoreV1().Pods(pod.Namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create pod %s/%s: %w", pod.Namespace, pod.Name, err)
	}
	return created, nil
}

// CreateOrUpdateSecret creates the named secret, or updates it if it already
// exists. Updates go through the fetched object so its ResourceVersion is
// preserved — the API server rejects updates that omit it.
func CreateOrUpdateSecret(ctx context.Context, client kubernetes.Interface, namespace, name string, data map[string][]byte) error {
	existing, err := client.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Data:       data,
		}
		_, err = client.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	case err == nil:
		existing.Data = data
		_, err = client.CoreV1().Secrets(namespace).Update(ctx, existing, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("failed to create/update secret %s/%s: %w", namespace, name, err)
	}
	return nil
}

// DeletePod deletes the pod if it exists; NotFound is not an error.
func DeletePod(ctx context.Context, client kubernetes.Interface, namespace, name string) error {
	err := client.CoreV1().Pods(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to delete pod %s/%s: %w", namespace, name, err)
	}
	return nil
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
