package kubernetes

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func nodeWithReady(name string, ready bool) *corev1.Node {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{
				Type:   corev1.NodeReady,
				Status: status,
			}},
		},
	}
}

var _ = Describe("CreateRebootPod", func() {
	It("creates a privileged reboot pod on the target node", func() {
		client := fake.NewClientset()

		created, err := CreateRebootPod(context.Background(), client, "worker-1", "ubi-minimal:latest")
		Expect(err).NotTo(HaveOccurred())
		Expect(created.Name).To(Equal(rebootPodPrefix + "worker-1"))

		pod, err := client.CoreV1().Pods(rebootPodNamespace).Get(
			context.Background(),
			rebootPodPrefix+"worker-1",
			metav1.GetOptions{},
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(pod.Spec.NodeName).To(Equal("worker-1"))
		Expect(pod.Spec.Containers[0].Image).To(Equal("ubi-minimal:latest"))
		Expect(pod.Spec.Containers[0].Command).To(Equal([]string{"chroot", "/host", "/sbin/reboot"}))
		Expect(*pod.Spec.Containers[0].SecurityContext.Privileged).To(BeTrue())
	})
})

var _ = Describe("WaitForNodesNotReady", func() {
	It("returns when every node is already NotReady", func() {
		client := fake.NewClientset(nodeWithReady("worker-1", false))

		err := WaitForNodesNotReady(context.Background(), client, []string{"worker-1"}, 5*time.Second, nil)
		Expect(err).NotTo(HaveOccurred())
	})

	It("times out while nodes stay Ready", func() {
		client := fake.NewClientset(nodeWithReady("worker-1", true))

		err := WaitForNodesNotReady(context.Background(), client, []string{"worker-1"}, 50*time.Millisecond, nil)
		Expect(err).To(MatchError(ContainSubstring("timed out")))
	})
})

var _ = Describe("ListPods", func() {
	It("lists pods in a namespace", func() {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "workload"},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning},
		}
		client := fake.NewClientset(pod)

		pods, err := ListPods(context.Background(), client, "workload")
		Expect(err).NotTo(HaveOccurred())
		Expect(pods).To(HaveLen(1))
		Expect(pods[0].Name).To(Equal("app"))
	})
})
