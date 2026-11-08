package kubernetes

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("CreatePod", func() {
	It("uses the default namespace when none is set", func() {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "test-pod"},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "c", Image: "img"}},
			},
		}
		client := fake.NewClientset()

		created, err := CreatePod(context.Background(), client, pod)
		Expect(err).NotTo(HaveOccurred())
		Expect(created.Namespace).To(Equal("default"))

		stored, err := client.CoreV1().Pods("default").Get(context.Background(), "test-pod", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(stored.Name).To(Equal("test-pod"))
	})
})

var _ = Describe("DeletePod", func() {
	It("deletes the pod even when the context is already cancelled", func() {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "img"}}},
		}
		client := fake.NewClientset(pod)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := DeletePod(ctx, client, "default", "test-pod")
		Expect(err).NotTo(HaveOccurred())

		_, err = client.CoreV1().Pods("default").Get(context.Background(), "test-pod", metav1.GetOptions{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	It("ignores a missing pod", func() {
		err := DeletePod(context.Background(), fake.NewClientset(), "default", "missing")
		Expect(err).NotTo(HaveOccurred())
	})
})

var _ = Describe("WaitForPodTerminal", func() {
	It("returns immediately when the pod already succeeded", func() {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "done", Namespace: "default"},
			Status:     corev1.PodStatus{Phase: corev1.PodSucceeded},
		}
		client := fake.NewClientset(pod)

		got, err := WaitForPodTerminal(PodWaitParams{
			Ctx: context.Background(), Client: client, Namespace: "default", Name: "done", Timeout: time.Minute,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Status.Phase).To(Equal(corev1.PodSucceeded))
	})

	It("returns an error when the pod failed", func() {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "bad", Namespace: "default"},
			Status:     corev1.PodStatus{Phase: corev1.PodFailed},
		}
		client := fake.NewClientset(pod)

		_, err := WaitForPodTerminal(PodWaitParams{
			Ctx: context.Background(), Client: client, Namespace: "default", Name: "bad", Timeout: time.Minute,
		})
		Expect(err).To(MatchError(ContainSubstring("failed")))
	})
})
