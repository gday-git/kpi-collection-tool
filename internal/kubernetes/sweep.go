package kubernetes

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// SweepManagedObjects deletes every pod and Secret carrying the tool's
// managed-by label, across all namespaces, and returns what was removed. It
// runs before a new collector creates anything, so leftovers from a prior run
// — whether kept deliberately (--nocleanup) or orphaned by SIGKILL — cannot
// collide with this run's objects. Any deletion failure is an error: the run
// refuses to start rather than proceeding into stale state. Only labeled
// objects are touched; unrelated workloads are never deleted.
func SweepManagedObjects(ctx context.Context, client kubernetes.Interface) ([]string, error) {
	opts := metav1.ListOptions{LabelSelector: ManagedBySelector}
	pods, err := client.CoreV1().Pods(metav1.NamespaceAll).List(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list leftover pods: %w", err)
	}
	secrets, err := client.CoreV1().Secrets(metav1.NamespaceAll).List(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list leftover secrets: %w", err)
	}

	var removed []string
	for i := range pods.Items {
		pod := &pods.Items[i]
		if err := DeletePod(ctx, client, pod.Namespace, pod.Name); err != nil {
			return removed, fmt.Errorf("failed to remove leftover pod %s/%s from a previous run: %w",
				pod.Namespace, pod.Name, err)
		}
		removed = append(removed, "pod "+pod.Namespace+"/"+pod.Name)
	}
	for i := range secrets.Items {
		secret := &secrets.Items[i]
		if err := DeleteSecret(ctx, client, secret.Namespace, secret.Name); err != nil {
			return removed, fmt.Errorf("failed to remove leftover secret %s/%s from a previous run: %w",
				secret.Namespace, secret.Name, err)
		}
		removed = append(removed, "secret "+secret.Namespace+"/"+secret.Name)
	}
	return removed, nil
}
