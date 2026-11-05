package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	authv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// mockK8sClient is a simple mock that implements the K8sClient interface
type mockK8sClient struct {
	getRouteRawFunc               func(ctx context.Context, path string) ([]byte, error)
	createServiceAccountTokenFunc func(ctx context.Context, namespace, serviceAccount string, tokenRequest *authv1.TokenRequest) (*authv1.TokenRequest, error)
}

func (m *mockK8sClient) GetRouteRaw(ctx context.Context, path string) ([]byte, error) {
	if m.getRouteRawFunc != nil {
		return m.getRouteRawFunc(ctx, path)
	}
	return nil, nil
}

func (m *mockK8sClient) CreateServiceAccountToken(ctx context.Context, namespace, serviceAccount string, tokenRequest *authv1.TokenRequest) (*authv1.TokenRequest, error) {
	if m.createServiceAccountTokenFunc != nil {
		return m.createServiceAccountTokenFunc(ctx, namespace, serviceAccount, tokenRequest)
	}
	return nil, nil
}

var _ = Describe("Kubernetes Client", func() {
	Describe("ClientsetFromKubeconfig", func() {
		var (
			tmpDir         string
			kubeconfigPath string
		)

		// Create a temporary directory and kubeconfig file for testing
		BeforeEach(func() {
			var err error
			// Create a temporary directory for test files
			tmpDir, err = os.MkdirTemp("", "k8s-client-test-*")
			Expect(err).NotTo(HaveOccurred())

			// Set the kubeconfig path in the temp directory
			kubeconfigPath = filepath.Join(tmpDir, "kubeconfig")
		})

		// Clean up temporary files created during testing
		AfterEach(func() {
			if tmpDir != "" {
				err := os.RemoveAll(tmpDir)
				Expect(err).NotTo(HaveOccurred())
			}
		})

		// given a path to a non-existent kubeconfig file
		It("should return an error when kubeconfig file does not exist", func() {
			nonExistentPath := filepath.Join(tmpDir, "nonexistent-kubeconfig")

			By("We try to setup a Kubernetes client with invalid kubeconfig path")
			clientset, err := ClientsetFromKubeconfig(nonExistentPath, "")

			// We should get an error
			Expect(err).To(HaveOccurred())
			// The clientset should be nil
			Expect(clientset).To(BeNil())
		})

		// given an invalid kubeconfig file
		It("should return an error when kubeconfig file is invalid", func() {
			invalidConfig := []byte("this is not valid yaml: {[}")
			err := os.WriteFile(kubeconfigPath, invalidConfig, 0644)
			Expect(err).NotTo(HaveOccurred())

			By("We try to setup a Kubernetes client with this invalid config")
			clientset, err := ClientsetFromKubeconfig(kubeconfigPath, "")

			// We should get an error
			Expect(err).To(HaveOccurred())
			// And: The clientset should be nil
			Expect(clientset).To(BeNil())
		})

		It("should successfully create a clientset with valid kubeconfig", func() {
			// A valid kubeconfig file (minimal but valid structure)
			// This creates a kubeconfig that points to a non-existent server,
			// but the structure is valid enough for the client creation to succeed
			validConfig := `
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://localhost:6443
    insecure-skip-tls-verify: true
  name: test-cluster
contexts:
- context:
    cluster: test-cluster
    user: test-user
  name: test-context
current-context: test-context
users:
- name: test-user
  user:
    token: test-token
`
			err := os.WriteFile(kubeconfigPath, []byte(validConfig), 0644)
			Expect(err).NotTo(HaveOccurred())

			By("We setup a Kubernetes client with this valid config")
			clientset, err := ClientsetFromKubeconfig(kubeconfigPath, "")

			// We should not get an error
			Expect(err).NotTo(HaveOccurred())
			// he clientset should not be nil
			Expect(clientset).NotTo(BeNil())
			// The clientset should be of the correct type
			Expect(clientset).To(BeAssignableToTypeOf(&kubernetes.Clientset{}))
			// clientset should be configured as the kubeconfig
			restConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(restConfig.Host).To(Equal("https://localhost:6443"))
			Expect(restConfig.BearerToken).To(Equal("test-token"))
			Expect(restConfig.Insecure).To(BeTrue())
		})
	})

	Describe("getThanosURL", func() {
		It("should successfully extract Thanos URL from route", func() {
			// A mock client that returns a valid route response (no actual server)
			mock := &mockK8sClient{
				getRouteRawFunc: func(ctx context.Context, path string) ([]byte, error) {
					// Verify the request is for the correct path
					Expect(path).To(Equal(THANOS_ROUTE_API_PATH))

					// Return a mock route response with a host
					routeResponse := map[string]interface{}{
						"spec": map[string]interface{}{
							"host": "thanos-querier-openshift-monitoring.apps.example.com",
						},
					}
					return json.Marshal(routeResponse)
				},
			}

			// We call getThanosURL
			thanosURL, err := getThanosURL(mock)

			// We should not get an error
			Expect(err).NotTo(HaveOccurred())
			// The URL should match what the mock returned
			Expect(thanosURL).To(Equal("thanos-querier-openshift-monitoring.apps.example.com"))
		})

		It("should return an error when route host is empty", func() {
			// A mock client that returns a route with an empty host (no actual server)
			mock := &mockK8sClient{
				getRouteRawFunc: func(ctx context.Context, path string) ([]byte, error) {
					routeResponse := map[string]interface{}{
						"spec": map[string]interface{}{
							"host": "",
						},
					}
					return json.Marshal(routeResponse)
				},
			}

			// We call getThanosURL
			thanosURL, err := getThanosURL(mock)

			// We should get an error
			Expect(err).To(HaveOccurred())
			// The URL should be empty
			Expect(thanosURL).To(BeEmpty())
		})

		It("should return an error when route response is invalid JSON", func() {
			// A mock client that returns invalid JSON (no actual server)
			mock := &mockK8sClient{
				getRouteRawFunc: func(ctx context.Context, path string) ([]byte, error) {
					return []byte("this is not valid json {[}"), nil
				},
			}

			// We call getThanosURL
			thanosURL, err := getThanosURL(mock)

			// We should get an error
			Expect(err).To(HaveOccurred())
			// The URL should be empty
			Expect(thanosURL).To(BeEmpty())
		})

		It("should return an error when API request fails", func() {
			// A mock client that returns an error (no actual server)
			mock := &mockK8sClient{
				getRouteRawFunc: func(ctx context.Context, path string) ([]byte, error) {
					return nil, fmt.Errorf("route not found")
				},
			}

			// We call getThanosURL
			thanosURL, err := getThanosURL(mock)

			// We should get an error
			Expect(err).To(HaveOccurred())
			// The URL should be empty
			Expect(thanosURL).To(BeEmpty())
		})

		It("should return an error when route response is missing spec field", func() {
			// A mock client that returns a route without a spec field (no actual server)
			mock := &mockK8sClient{
				getRouteRawFunc: func(ctx context.Context, path string) ([]byte, error) {
					routeResponse := map[string]interface{}{
						"metadata": map[string]interface{}{
							"name": "thanos-querier",
						},
					}
					return json.Marshal(routeResponse)
				},
			}

			// We call getThanosURL
			thanosURL, err := getThanosURL(mock)

			// We should get an error (empty host)
			Expect(err).To(HaveOccurred())
			// The error message should indicate host extraction failed
			Expect(err.Error()).To(ContainSubstring("failed to extract host from route spec"))
			// The URL should be empty
			Expect(thanosURL).To(BeEmpty())
		})
	})

	Describe("createServiceAccountToken", func() {
		It("should successfully create and return a service account token", func() {
			requestedDuration := 45 * time.Minute

			mock := &mockK8sClient{
				createServiceAccountTokenFunc: func(ctx context.Context, namespace, serviceAccount string, tokenRequest *authv1.TokenRequest) (*authv1.TokenRequest, error) {
					Expect(namespace).To(Equal(MonitoringNamespace))
					Expect(serviceAccount).To(Equal(TokenServiceAccountName))
					Expect(*tokenRequest.Spec.ExpirationSeconds).To(Equal(int64(requestedDuration.Seconds())))

					return &authv1.TokenRequest{
						Status: authv1.TokenRequestStatus{
							Token: "mock-service-account-token-12345",
							ExpirationTimestamp: metav1.Time{
								Time: metav1.Now().Add(requestedDuration),
							},
						},
					}, nil
				},
			}

			token, err := createServiceAccountToken(mock, requestedDuration)

			Expect(err).NotTo(HaveOccurred())
			Expect(token).To(Equal("mock-service-account-token-12345"))
		})

		It("should return an error when service account does not exist", func() {
			mock := &mockK8sClient{
				createServiceAccountTokenFunc: func(ctx context.Context, namespace, serviceAccount string, tokenRequest *authv1.TokenRequest) (*authv1.TokenRequest, error) {
					return nil, fmt.Errorf("serviceaccount not found")
				},
			}

			token, err := createServiceAccountToken(mock, time.Hour)

			Expect(err).To(HaveOccurred())
			Expect(token).To(BeEmpty())
		})

		It("should return an error when API returns empty token", func() {
			mock := &mockK8sClient{
				createServiceAccountTokenFunc: func(ctx context.Context, namespace, serviceAccount string, tokenRequest *authv1.TokenRequest) (*authv1.TokenRequest, error) {
					return &authv1.TokenRequest{
						Status: authv1.TokenRequestStatus{
							Token: "",
						},
					}, nil
				},
			}

			token, err := createServiceAccountToken(mock, time.Hour)

			Expect(err).NotTo(HaveOccurred())
			Expect(token).To(BeEmpty())
		})
	})

	Describe("SetupKubeconfigAuth", func() {
		var (
			tmpDir         string
			kubeconfigPath string
		)

		// Create a temporary directory for test files
		BeforeEach(func() {
			var err error
			tmpDir, err = os.MkdirTemp("", "k8s-auth-test-*")
			Expect(err).NotTo(HaveOccurred())

			kubeconfigPath = filepath.Join(tmpDir, "kubeconfig")
		})

		// Clean up temporary files
		AfterEach(func() {
			if tmpDir != "" {
				err := os.RemoveAll(tmpDir)
				Expect(err).NotTo(HaveOccurred())
			}
		})

		It("should return an error when kubeconfig file does not exist", func() {
			nonExistentPath := filepath.Join(tmpDir, "nonexistent")

			thanosURL, token, err := SetupKubeconfigAuth(nonExistentPath, "", time.Hour)

			Expect(err).To(HaveOccurred())
			Expect(thanosURL).To(BeEmpty())
			Expect(token).To(BeEmpty())
		})

		It("should return an error when kubeconfig is invalid", func() {
			invalidConfig := []byte("invalid yaml {[}")
			err := os.WriteFile(kubeconfigPath, invalidConfig, 0644)
			Expect(err).NotTo(HaveOccurred())

			thanosURL, token, err := SetupKubeconfigAuth(kubeconfigPath, "", time.Hour)

			Expect(err).To(HaveOccurred())
			Expect(thanosURL).To(BeEmpty())
			Expect(token).To(BeEmpty())
		})

		It("should return an error when unable to connect to cluster", func() {
			validConfig := `
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://localhost:9999
    insecure-skip-tls-verify: true
  name: test-cluster
contexts:
- context:
    cluster: test-cluster
    user: test-user
  name: test-context
current-context: test-context
users:
- name: test-user
  user:
    token: test-token
`
			err := os.WriteFile(kubeconfigPath, []byte(validConfig), 0644)
			Expect(err).NotTo(HaveOccurred())

			thanosURL, token, err := SetupKubeconfigAuth(kubeconfigPath, "", time.Hour)

			Expect(err).To(HaveOccurred())
			Expect(thanosURL).To(BeEmpty())
			Expect(token).To(BeEmpty())
		})
	})

})
