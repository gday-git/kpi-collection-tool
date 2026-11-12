package task

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("objectTracker", func() {
	It("deletes exactly the tracked pods and secrets", func() {
		trackedPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "tracked", Namespace: "default"}}
		trackedSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "tracked", Namespace: "default"}}
		otherPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "default"}}
		client := fake.NewClientset(trackedPod, trackedSecret, otherPod)

		var tracker objectTracker
		tracker.Add("pod", "default", "tracked")
		tracker.Add("secret", "default", "tracked")
		tracker.DeleteAll(context.Background(), client, "test-task")

		_, err := client.CoreV1().Pods("default").Get(context.Background(), "tracked", metav1.GetOptions{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
		_, err = client.CoreV1().Secrets("default").Get(context.Background(), "tracked", metav1.GetOptions{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
		_, err = client.CoreV1().Pods("default").Get(context.Background(), "other", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
	})

	It("treats already-deleted objects as no-ops", func() {
		client := fake.NewClientset()

		var tracker objectTracker
		tracker.Add("pod", "default", "never-existed")
		tracker.DeleteAll(context.Background(), client, "test-task")
	})
})
