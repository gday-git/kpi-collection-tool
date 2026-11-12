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

var _ = Describe("CreateOwnedSecret", func() {
	ours := map[string]string{ManagedByLabel: ManagedByValue}

	It("creates the secret when it does not exist", func() {
		client := fake.NewClientset()

		name, err := CreateOwnedSecret(context.Background(), client, "default", "sec", map[string][]byte{"k": []byte("v1")}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(name).To(Equal("sec"))

		sec, err := client.CoreV1().Secrets("default").Get(context.Background(), "sec", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(sec.Data).To(HaveKeyWithValue("k", []byte("v1")))
	})

	It("updates an owned secret in place, preserving its resourceVersion", func() {
		client := fake.NewClientset()
		name, err := CreateOwnedSecret(context.Background(), client, "default", "sec", map[string][]byte{"k": []byte("v1")}, ours)
		Expect(err).NotTo(HaveOccurred())
		Expect(name).To(Equal("sec"))
		before, err := client.CoreV1().Secrets("default").Get(context.Background(), "sec", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())

		// The retry-run scenario: the secret already exists with a
		// resourceVersion, and the update must carry it.
		name, err = CreateOwnedSecret(context.Background(), client, "default", "sec", map[string][]byte{"k": []byte("v2")}, ours)
		Expect(err).NotTo(HaveOccurred())
		Expect(name).To(Equal("sec"))

		after, err := client.CoreV1().Secrets("default").Get(context.Background(), "sec", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(after.Data).To(HaveKeyWithValue("k", []byte("v2")))
		Expect(after.ResourceVersion).To(Equal(before.ResourceVersion))
	})

	It("allocates the next free name when a foreign secret holds the base name", func() {
		foreign := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "sec",
				Namespace: "default",
				Labels:    map[string]string{"team": "someone-else"},
			},
			Data: map[string][]byte{"k": []byte("foreign")},
		}
		client := fake.NewClientset(foreign)

		name, err := CreateOwnedSecret(context.Background(), client, "default", "sec", map[string][]byte{"k": []byte("v1")}, ours)
		Expect(err).NotTo(HaveOccurred())
		Expect(name).To(Equal("sec-2"))

		// The new secret is ours, labeled and filled with our data.
		sec, err := client.CoreV1().Secrets("default").Get(context.Background(), "sec-2", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(sec.Data).To(HaveKeyWithValue("k", []byte("v1")))
		Expect(sec.Labels).To(HaveKeyWithValue(ManagedByLabel, ManagedByValue))

		// The foreign secret is untouched — data and labels intact.
		untouched, err := client.CoreV1().Secrets("default").Get(context.Background(), "sec", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(untouched.Data).To(HaveKeyWithValue("k", []byte("foreign")))
		Expect(untouched.Labels).To(HaveLen(1))
		Expect(untouched.Labels).To(HaveKeyWithValue("team", "someone-else"))
	})
})

var _ = Describe("DeleteSecret", func() {
	It("deletes an existing secret", func() {
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "sec", Namespace: "default"}}
		client := fake.NewClientset(secret)

		Expect(DeleteSecret(context.Background(), client, "default", "sec")).To(Succeed())

		_, err := client.CoreV1().Secrets("default").Get(context.Background(), "sec", metav1.GetOptions{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	It("treats a missing secret as a no-op", func() {
		client := fake.NewClientset()
		Expect(DeleteSecret(context.Background(), client, "default", "never-existed")).To(Succeed())
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
