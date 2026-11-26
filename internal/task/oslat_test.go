package task

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/config"
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

		secretName, err := t.injectKubeconfig(context.Background(), client, pod)
		Expect(err).NotTo(HaveOccurred())
		Expect(secretName).To(Equal("oslat-test-kubeconfig"))

		// Secret created in the resolved (default) namespace with the kubeconfig data.
		Expect(pod.Namespace).To(Equal("default"))
		sec, err := client.CoreV1().Secrets("default").Get(context.Background(), "oslat-test-kubeconfig", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(sec.Data).To(HaveKey("kubeconfig"))

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

		_, err := t.injectKubeconfig(context.Background(), client, pod)
		Expect(err).NotTo(HaveOccurred())

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

		_, err := t.injectKubeconfig(context.Background(), client, pod)
		Expect(err).NotTo(HaveOccurred())

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

		_, err := t.injectKubeconfig(context.Background(), client, pod)
		Expect(err).To(MatchError(ContainSubstring(`volume "kubeconfig" already defined`)))

		// Validation runs before creation, so a rejected spec leaves no secret.
		secrets, listErr := client.CoreV1().Secrets("default").List(context.Background(), metav1.ListOptions{})
		Expect(listErr).NotTo(HaveOccurred())
		Expect(secrets.Items).To(BeEmpty())
	})

	It("errors when a container already defines a kubeconfig volumeMount", func() {
		pod.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{{Name: "kubeconfig", MountPath: "/other"}}
		client := fake.NewClientset()
		t := NewOslatTask(config.OslatTaskConfig{MountKubeconfig: true}, kubeconfigPath, "", "/tmp")

		_, err := t.injectKubeconfig(context.Background(), client, pod)
		Expect(err).To(MatchError(ContainSubstring(`volumeMount "kubeconfig" already defined`)))

		secrets, listErr := client.CoreV1().Secrets("default").List(context.Background(), metav1.ListOptions{})
		Expect(listErr).NotTo(HaveOccurred())
		Expect(secrets.Items).To(BeEmpty())
	})

	It("errors when no kubeconfig path is provided", func() {
		t := NewOslatTask(config.OslatTaskConfig{MountKubeconfig: true}, "", "", "/tmp")
		_, err := t.injectKubeconfig(context.Background(), fake.NewClientset(), pod)
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("OslatTask Run secret cleanup", func() {
	It("deletes the kubeconfig secret when pod creation fails", func() {
		var mu sync.Mutex
		deleted := []string{}
		notFound := `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/secrets/"):
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(notFound))
			case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/secrets"):
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{}`))
			case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/secrets/"):
				mu.Lock()
				deleted = append(deleted, r.URL.Path)
				mu.Unlock()
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{}`))
			case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pods"):
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"InternalError","code":500,"message":"pod create rejected"}`))
			default:
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(notFound))
			}
		}))
		defer srv.Close()

		// Kubeconfig fixture pointing at the test API server; it doubles as the
		// file mountKubeconfig turns into a Secret.
		dir := GinkgoT().TempDir()
		kcPath := filepath.Join(dir, "kubeconfig")
		kubeconfig := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: %s
    insecure-skip-tls-verify: true
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user:
    token: test
`, srv.URL)
		Expect(os.WriteFile(kcPath, []byte(kubeconfig), 0600)).To(Succeed())

		t := NewOslatTask(config.OslatTaskConfig{
			MountKubeconfig: true,
			Pod: &config.Pod{
				Pod: corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "oslat-clean"},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "img"}}},
				},
			},
		}, kcPath, "", dir)
		t.cleanup = true

		// CreatePod fails against the test server; the cleanup defer must
		// still delete the secret the task created before that failure.
		err := t.Run(context.Background())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to create pod"))

		mu.Lock()
		defer mu.Unlock()
		Expect(deleted).To(ConsistOf("/api/v1/namespaces/default/secrets/oslat-clean-kubeconfig"))
	})
})
