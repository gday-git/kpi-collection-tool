// Package kubernetes provides integration with Kubernetes/OpenShift clusters.
// It handles kubeconfig-based authentication, automatic discovery of Thanos
// querier routes, and creation of service account tokens for Prometheus API
// access in OpenShift monitoring namespaces.
package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	authv1 "k8s.io/api/authentication/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	THANOS_ROUTE_API_PATH   = "/apis/route.openshift.io/v1/namespaces/openshift-monitoring/routes/thanos-querier"
	MonitoringNamespace     = "openshift-monitoring"
	TokenServiceAccountName = "prometheus-k8s"
)

// SetupKubeconfigAuth sets up authentication using kubeconfig file,
// discovers the Thanos URL, and creates a service account token
// whose lifetime matches tokenDuration. proxyURL, when non-empty, routes
// the API traffic through the given proxy (overriding any kubeconfig proxy-url).
func SetupKubeconfigAuth(kubeconfig, proxyURL string, tokenDuration time.Duration) (string, string, error) {
	clientset, err := ClientsetFromKubeconfig(kubeconfig, proxyURL)
	if err != nil {
		return "", "", fmt.Errorf("failed to setup Kubernetes client: %v", err)
	}

	client := &k8sClientImpl{clientset: clientset}

	thanosURL, err := getThanosURL(client)
	if err != nil {
		return "", "", fmt.Errorf("failed to get Thanos URL: %v", err)
	}

	bearerToken, err := createServiceAccountToken(client, tokenDuration)
	if err != nil {
		return "", "", fmt.Errorf("failed to create service account token: %v", err)
	}

	return thanosURL, bearerToken, nil
}

// ClientsetFromKubeconfig builds a clientset from a kubeconfig file path.
// When proxyURL is non-empty, all API traffic is routed through it (overriding
// any proxy-url set in the kubeconfig); an empty proxyURL leaves the kubeconfig's
// own proxy settings untouched.
func ClientsetFromKubeconfig(kubeconfigPath, proxyURL string) (*kubernetes.Clientset, error) {
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig: %v", err)
	}

	if proxyURL != "" {
		u, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy URL %q: %w", proxyURL, err)
		}
		config.Proxy = http.ProxyURL(u)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %v", err)
	}

	return clientset, nil
}

// getThanosURL retrieves the Thanos querier route hostname from OpenShift
// Equivalent to: oc get route thanos-querier -n openshift-monitoring -o jsonpath='{.spec.host}'
func getThanosURL(client K8sClient) (string, error) {
	routes, err := client.GetRouteRaw(context.TODO(), THANOS_ROUTE_API_PATH)
	if err != nil {
		return "", fmt.Errorf("failed to get thanos-querier route: %v", err)
	}

	var route route
	if err := json.Unmarshal(routes, &route); err != nil {
		return "", fmt.Errorf("failed to parse route response: %v", err)
	}

	if route.Spec.Host != "" {
		return route.Spec.Host, nil
	}

	return "", fmt.Errorf("failed to extract host from route spec")
}

// createServiceAccountToken creates a service account token for authentication
// with the given expiration duration.
func createServiceAccountToken(client K8sClient, expiration time.Duration) (string, error) {
	expirationSeconds := int64(expiration.Seconds())

	tokenRequest := &authv1.TokenRequest{
		Spec: authv1.TokenRequestSpec{
			ExpirationSeconds: &expirationSeconds,
		},
	}

	result, err := client.CreateServiceAccountToken(
		context.TODO(),
		MonitoringNamespace,
		TokenServiceAccountName,
		tokenRequest,
	)
	if err != nil {
		return "", fmt.Errorf("failed to create service account token: %v", err)
	}

	return result.Status.Token, nil
}
