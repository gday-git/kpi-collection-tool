package kubernetes

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func labeledPod(name, namespace string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{ManagedByLabel: ManagedByValue, RunIDLabel: "run-old"},
		},
	}
}

var _ = Describe("SweepManagedObjects", func() {
	It("deletes labeled pods and secrets across namespaces, leaving unlabeled objects", func() {
		labeledSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "oslat-kubeconfig",
				Namespace: "other-ns",
				Labels:    map[string]string{ManagedByLabel: ManagedByValue, RunIDLabel: "run-old"},
			},
		}
		unlabeledPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "someone-elses", Namespace: "default"}}
		unlabeledSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "mine", Namespace: "default"}}
		client := fake.NewClientset(
			labeledPod("pnd-node-1", "default"),
			labeledPod("oslat-run-2", "other-ns"),
			labeledSecret,
			unlabeledPod,
			unlabeledSecret,
		)

		removed, err := SweepManagedObjects(context.Background(), client)
		Expect(err).NotTo(HaveOccurred())
		Expect(removed).To(ConsistOf(
			"pod default/pnd-node-1",
			"pod other-ns/oslat-run-2",
			"secret other-ns/oslat-kubeconfig",
		))

		// Labeled objects are gone, including in other namespaces.
		_, err = client.CoreV1().Pods("default").Get(context.Background(), "pnd-node-1", metav1.GetOptions{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
		_, err = client.CoreV1().Secrets("other-ns").Get(context.Background(), "oslat-kubeconfig", metav1.GetOptions{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())

		// Unlabeled objects are untouched.
		_, err = client.CoreV1().Pods("default").Get(context.Background(), "someone-elses", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		_, err = client.CoreV1().Secrets("default").Get(context.Background(), "mine", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
	})

	It("returns nothing when there are no leftovers", func() {
		client := fake.NewClientset()

		removed, err := SweepManagedObjects(context.Background(), client)
		Expect(err).NotTo(HaveOccurred())
		Expect(removed).To(BeEmpty())
	})
})

var _ = Describe("LabelObject", func() {
	It("stamps ownership labels without clobbering existing ones", func() {
		meta := &metav1.ObjectMeta{
			Labels: map[string]string{"user-label": "keep"},
		}

		LabelObject(meta, "run-42")

		Expect(meta.Labels).To(HaveKeyWithValue(ManagedByLabel, ManagedByValue))
		Expect(meta.Labels).To(HaveKeyWithValue(RunIDLabel, "run-42"))
		Expect(meta.Labels).To(HaveKeyWithValue("user-label", "keep"))
	})
})
