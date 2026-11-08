package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8snet "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/client-go/kubernetes"
)

const (
	rebootPodPrefix    = "kpi-app-recovery-reboot-"
	rebootPodNamespace = defaultNamespace
	apiCallTimeout     = 15 * time.Second
)

// CheckAppRecoveryAccess verifies the kubeconfig can reboot nodes and list workload pods.
// Call before the task runs so weak credentials fail early.
func CheckAppRecoveryAccess(ctx context.Context, client kubernetes.Interface, nodeNames, workloadNamespaces []string, image string) error {
	var problems []string

	for _, nodeName := range nodeNames {
		if _, err := client.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{}); err != nil {
			problems = append(problems, fmt.Sprintf("get node %q: %v", nodeName, err))
			continue
		}
		if err := dryRunRebootPod(ctx, client, nodeName, image); err != nil {
			problems = append(problems, fmt.Sprintf("create privileged reboot pod on node %q: %v", nodeName, err))
		}
	}

	for _, ns := range workloadNamespaces {
		allowed, reason, err := canAccess(ctx, client, "list", "pods", ns, "")
		if err != nil {
			problems = append(problems, fmt.Sprintf("check list pods in namespace %q: %v", ns, err))
			continue
		}
		if !allowed {
			msg := fmt.Sprintf("list pods in namespace %q", ns)
			if reason != "" {
				msg += ": " + reason
			}
			problems = append(problems, msg)
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf(
			"insufficient Kubernetes permissions (cluster-admin or equivalent required): %s",
			strings.Join(problems, "; "),
		)
	}
	return nil
}

func canAccess(ctx context.Context, client kubernetes.Interface, verb, resource, namespace, name string) (bool, string, error) {
	review, err := client.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authv1.SelfSubjectAccessReview{
		Spec: authv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authv1.ResourceAttributes{
				Verb:      verb,
				Resource:  resource,
				Namespace: namespace,
				Name:      name,
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return false, "", err
	}
	return review.Status.Allowed, review.Status.Reason, nil
}

func dryRunRebootPod(ctx context.Context, client kubernetes.Interface, nodeName, image string) error {
	_, err := client.CoreV1().Pods(rebootPodNamespace).Create(
		ctx,
		newRebootPod(nodeName, image),
		metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}},
	)
	return err
}

func newRebootPod(nodeName, image string) *corev1.Pod {
	privileged := true
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rebootPodPrefix + nodeName,
			Namespace: rebootPodNamespace,
		},
		Spec: corev1.PodSpec{
			NodeName:      nodeName,
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name:    "reboot",
				Image:   image,
				Command: []string{"chroot", "/host", "/sbin/reboot"},
				SecurityContext: &corev1.SecurityContext{
					Privileged: &privileged,
				},
				VolumeMounts: []corev1.VolumeMount{{
					Name:      "host",
					MountPath: "/host",
				}},
			}},
			Volumes: []corev1.Volume{{
				Name: "host",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{Path: "/"},
				},
			}},
		},
	}
}

// CreateRebootPod schedules a privileged pod on nodeName that reboots the host.
// The pod is not waited on after create. The returned pod is the one to delete
// when orchestration.cleanup is set.
func CreateRebootPod(ctx context.Context, client kubernetes.Interface, nodeName, image string) (*corev1.Pod, error) {
	created, err := CreatePod(ctx, client, newRebootPod(nodeName, image))
	if err != nil {
		return nil, fmt.Errorf("reboot pod on node %s: %w", nodeName, err)
	}
	return created, nil
}

// WaitForNodesNotReady polls until every node in nodeNames is NotReady, the API
// is unreachable (SNO / control-plane reboot), or timeout expires.
// onNodeNotReady is invoked at most once per node when it is first observed NotReady
// or its status cannot be fetched because the API is unreachable.
func WaitForNodesNotReady(
	ctx context.Context,
	client kubernetes.Interface,
	nodeNames []string,
	timeout time.Duration,
	onNodeNotReady func(string),
) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	reported := make(map[string]bool, len(nodeNames))
	ticker := time.NewTicker(podPollInterval)
	defer ticker.Stop()

	for {
		allNotReady, err := nodesNotReady(waitCtx, client, nodeNames, reported, onNodeNotReady)
		if err != nil {
			return err
		}
		if allNotReady {
			return nil
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("timed out waiting for nodes %v to become NotReady: %w", nodeNames, waitCtx.Err())
		case <-ticker.C:
		}
	}
}

func nodesNotReady(
	ctx context.Context,
	client kubernetes.Interface,
	nodeNames []string,
	reported map[string]bool,
	onNodeNotReady func(string),
) (bool, error) {
	for _, name := range nodeNames {
		callCtx, cancel := context.WithTimeout(ctx, apiCallTimeout)
		node, err := client.CoreV1().Nodes().Get(callCtx, name, metav1.GetOptions{})
		cancel()
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if err != nil {
			if isAPIUnreachable(err) {
				markNodeNotReady(name, reported, onNodeNotReady)
				continue
			}
			return false, fmt.Errorf("failed to get node %s: %w", name, err)
		}
		if nodeIsReady(node) {
			return false, nil
		}
		markNodeNotReady(name, reported, onNodeNotReady)
	}
	return true, nil
}

func markNodeNotReady(name string, reported map[string]bool, onNodeNotReady func(string)) {
	if onNodeNotReady == nil || reported[name] {
		return
	}
	reported[name] = true
	onNodeNotReady(name)
}

func isAPIUnreachable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	return apierrors.IsTimeout(err) ||
		apierrors.IsServerTimeout(err) ||
		apierrors.IsServiceUnavailable(err) ||
		apierrors.IsInternalError(err) ||
		k8snet.IsProbableEOF(err) ||
		k8snet.IsConnectionReset(err) ||
		k8snet.IsConnectionRefused(err)
}

func nodeIsReady(node *corev1.Node) bool {
	for _, c := range node.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// ListPods returns pods in namespace.
func ListPods(ctx context.Context, client kubernetes.Interface, namespace string) ([]corev1.Pod, error) {
	callCtx, cancel := context.WithTimeout(ctx, apiCallTimeout)
	defer cancel()
	list, err := client.CoreV1().Pods(namespace).List(callCtx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods in %s: %w", namespace, err)
	}
	return list.Items, nil
}
