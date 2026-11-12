package task

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/config"
	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/kubernetes"
)

var _ = Describe("OslatTask injectKubeconfig", func() {
	var (
		kubeconfigPath string
		pod            *corev1.Pod
	)

	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		kubeconfigPath = filepath.Join(dir, "kubeconfig")
		Expect(os.WriteFile(kubeconfigPath, []byte("apiVersion: v1\nkind: Config\n"), 0600)).To(Succeed())

		pod = &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "oslat-test"},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "oslat", Image: "img"}},
			},
		}
	})

	It("creates a secret and injects volume, mount, and KUBECONFIG env", func() {
		client := fake.NewClientset()
		t := NewOslatTask(config.OslatTaskConfig{MountKubeconfig: true}, kubeconfigPath, "", "/tmp")

		Expect(t.injectKubeconfig(context.Background(), client, pod)).To(Succeed())

		// Secret created in the resolved (default) namespace with the kubeconfig data.
		Expect(pod.Namespace).To(Equal("default"))
		sec, err := client.CoreV1().Secrets("default").Get(context.Background(), "oslat-test-kubeconfig", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(sec.Data).To(HaveKey("kubeconfig"))
		Expect(sec.Labels).To(HaveKeyWithValue(kubernetes.ManagedByLabel, kubernetes.ManagedByValue))
		Expect(sec.Labels).To(HaveKeyWithValue(kubernetes.RunIDLabel, t.runID))

		// Volume wired to the secret.
		Expect(pod.Spec.Volumes).To(HaveLen(1))
		Expect(pod.Spec.Volumes[0].Secret.SecretName).To(Equal("oslat-test-kubeconfig"))

		// Container got the mount and the KUBECONFIG env at the default path.
		c := pod.Spec.Containers[0]
		Expect(c.VolumeMounts).To(HaveLen(1))
		Expect(c.VolumeMounts[0].MountPath).To(Equal("/kubeconfig"))
		Expect(c.Env).To(ContainElement(corev1.EnvVar{Name: "KUBECONFIG", Value: "/kubeconfig/kubeconfig"}))
	})

	It("honors a custom mountPath and does not overwrite an existing KUBECONFIG env", func() {
		pod.Spec.Containers[0].Env = []corev1.EnvVar{{Name: "KUBECONFIG", Value: "/preset"}}
		client := fake.NewClientset()
		t := NewOslatTask(config.OslatTaskConfig{MountKubeconfig: true, MountPath: "/kc"}, kubeconfigPath, "", "/tmp")

		Expect(t.injectKubeconfig(context.Background(), client, pod)).To(Succeed())

		c := pod.Spec.Containers[0]
		Expect(c.VolumeMounts[0].MountPath).To(Equal("/kc"))
		Expect(c.Env).To(ContainElement(corev1.EnvVar{Name: "KUBECONFIG", Value: "/preset"}))
	})

	It("patches init containers with the mount and KUBECONFIG env", func() {
		pod.Spec.InitContainers = []corev1.Container{
			{Name: "init", Image: "img"},
			{Name: "preset", Image: "img", Env: []corev1.EnvVar{{Name: "KUBECONFIG", Value: "/preset"}}},
		}
		client := fake.NewClientset()
		t := NewOslatTask(config.OslatTaskConfig{MountKubeconfig: true}, kubeconfigPath, "", "/tmp")

		Expect(t.injectKubeconfig(context.Background(), client, pod)).To(Succeed())

		init := pod.Spec.InitContainers[0]
		Expect(init.VolumeMounts[0].MountPath).To(Equal("/kubeconfig"))
		Expect(init.Env).To(ContainElement(corev1.EnvVar{Name: "KUBECONFIG", Value: "/kubeconfig/kubeconfig"}))

		preset := pod.Spec.InitContainers[1]
		Expect(preset.VolumeMounts[0].MountPath).To(Equal("/kubeconfig"))
		Expect(preset.Env).To(ContainElement(corev1.EnvVar{Name: "KUBECONFIG", Value: "/preset"}))
	})

	It("errors when the pod spec already defines a kubeconfig volume", func() {
		pod.Spec.Volumes = []corev1.Volume{{Name: "kubeconfig"}}
		client := fake.NewClientset()
		t := NewOslatTask(config.OslatTaskConfig{MountKubeconfig: true}, kubeconfigPath, "", "/tmp")

		err := t.injectKubeconfig(context.Background(), client, pod)
		Expect(err).To(MatchError(ContainSubstring(`volume "kubeconfig" already defined`)))

		// The spec error must fire before any secret is created in the cluster.
		secrets, listErr := client.CoreV1().Secrets("default").List(context.Background(), metav1.ListOptions{})
		Expect(listErr).NotTo(HaveOccurred())
		Expect(secrets.Items).To(BeEmpty())
		Expect(t.tracker.objects).To(BeEmpty())
	})

	It("mounts a uniquely named secret when a foreign secret holds the base name", func() {
		foreign := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "oslat-test-kubeconfig",
				Namespace: "default",
				Labels:    map[string]string{"team": "someone-else"},
			},
			Data: map[string][]byte{"kubeconfig": []byte("foreign")},
		}
		client := fake.NewClientset(foreign)
		t := NewOslatTask(config.OslatTaskConfig{MountKubeconfig: true}, kubeconfigPath, "", "/tmp")

		Expect(t.injectKubeconfig(context.Background(), client, pod)).To(Succeed())

		// The pod's volume points at the allocated variant, not the foreign secret.
		Expect(pod.Spec.Volumes).To(HaveLen(1))
		Expect(pod.Spec.Volumes[0].Secret.SecretName).To(Equal("oslat-test-kubeconfig-2"))

		// The tracker follows the actual name, so cleanup deletes the right secret.
		Expect(t.tracker.objects).To(ContainElement(trackedRef{kind: "secret", namespace: "default", name: "oslat-test-kubeconfig-2"}))

		// The foreign secret keeps its data and labels.
		untouched, err := client.CoreV1().Secrets("default").Get(context.Background(), "oslat-test-kubeconfig", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(untouched.Data).To(HaveKeyWithValue("kubeconfig", []byte("foreign")))
		Expect(untouched.Labels).To(HaveKeyWithValue("team", "someone-else"))
	})

	It("errors when no kubeconfig path is provided", func() {
		t := NewOslatTask(config.OslatTaskConfig{MountKubeconfig: true}, "", "", "/tmp")
		err := t.injectKubeconfig(context.Background(), fake.NewClientset(), pod)
		Expect(err).To(HaveOccurred())
	})
})
