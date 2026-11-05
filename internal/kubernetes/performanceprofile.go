package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"

	"k8s.io/client-go/kubernetes"
)

const (
	performanceProfileAPIPath = "/apis/performance.openshift.io/v2/performanceprofiles"
)

// PerformanceProfile represents the relevant parts of a PerformanceProfile CR
type PerformanceProfile struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		CPU struct {
			Reserved string `json:"reserved"`
			Isolated string `json:"isolated"`
		} `json:"cpu"`
	} `json:"spec"`
}

// performanceProfileList represents a list of PerformanceProfiles
type performanceProfileList struct {
	Items []PerformanceProfile `json:"items"`
}

// FetchCPUsFromPerformanceProfiles fetches all PerformanceProfiles from the cluster and returns
// aggregated reserved and isolated CPU IDs in Prometheus regex format (e.g., "0|1|32|33")
func FetchCPUsFromPerformanceProfiles(kubeconfigPath, proxyURL string) (reservedCPUs, isolatedCPUs string, err error) {
	clientset, err := ClientsetFromKubeconfig(kubeconfigPath, proxyURL)
	if err != nil {
		return "", "", fmt.Errorf("failed to setup Kubernetes client: %v", err)
	}

	profiles, err := fetchAllPerformanceProfiles(clientset)
	if err != nil {
		return "", "", fmt.Errorf("failed to fetch PerformanceProfiles: %v", err)
	}

	if len(profiles) == 0 {
		return "", "", fmt.Errorf("no PerformanceProfile found in cluster")
	}

	log.Printf("Found %d PerformanceProfile(s)", len(profiles))

	return aggregateCPUs(profiles)
}

// fetchAllPerformanceProfiles retrieves all PerformanceProfiles from the cluster
func fetchAllPerformanceProfiles(clientset *kubernetes.Clientset) ([]PerformanceProfile, error) {
	data, err := clientset.RESTClient().
		Get().
		AbsPath(performanceProfileAPIPath).
		DoRaw(context.TODO())
	if err != nil {
		return nil, fmt.Errorf("failed to get PerformanceProfiles: %v", err)
	}

	var list performanceProfileList
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("failed to parse PerformanceProfile list: %v", err)
	}

	return list.Items, nil
}

// aggregateCPUs combines CPU sets from all profiles and returns Prometheus regex strings
func aggregateCPUs(profiles []PerformanceProfile) (reservedCPUs, isolatedCPUs string, err error) {
	reservedSet := make(map[int]struct{})
	isolatedSet := make(map[int]struct{})

	for _, profile := range profiles {
		reserved, err := parseCPURange(profile.Spec.CPU.Reserved)
		if err != nil {
			return "", "", fmt.Errorf("failed to parse reserved CPUs in profile '%s': %v",
				profile.Metadata.Name, err)
		}

		isolated, err := parseCPURange(profile.Spec.CPU.Isolated)
		if err != nil {
			return "", "", fmt.Errorf("failed to parse isolated CPUs in profile '%s': %v",
				profile.Metadata.Name, err)
		}

		for _, cpuID := range reserved {
			reservedSet[cpuID] = struct{}{}
		}
		for _, cpuID := range isolated {
			isolatedSet[cpuID] = struct{}{}
		}

		log.Printf("  Profile '%s': reserved=[%s], isolated=[%s]",
			profile.Metadata.Name, profile.Spec.CPU.Reserved, profile.Spec.CPU.Isolated)
	}

	return cpuIDsToPrometheusRegex(reservedSet), cpuIDsToPrometheusRegex(isolatedSet), nil
}

// cpuIDsToPrometheusRegex converts a set of CPU IDs to Prometheus regex format "0|1|32|33"
func cpuIDsToPrometheusRegex(cpuSet map[int]struct{}) string {
	if len(cpuSet) == 0 {
		return ""
	}

	cpuIDs := make([]int, 0, len(cpuSet))
	for cpuID := range cpuSet {
		cpuIDs = append(cpuIDs, cpuID)
	}
	sort.Ints(cpuIDs)

	cpuStrings := make([]string, len(cpuIDs))
	for i, cpuID := range cpuIDs {
		cpuStrings[i] = strconv.Itoa(cpuID)
	}
	return strings.Join(cpuStrings, "|")
}

// parseCPURange converts CPU range string like "0-3,8-11" to []int{0,1,2,3,8,9,10,11}
func parseCPURange(cpuRange string) ([]int, error) {
	if cpuRange == "" {
		return nil, nil
	}

	var cpuIDs []int
	parts := strings.Split(cpuRange, ",")

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.Contains(part, "-") {
			bounds := strings.Split(part, "-")
			if len(bounds) != 2 {
				return nil, fmt.Errorf("invalid CPU range format: %s", part)
			}

			start, err := strconv.Atoi(strings.TrimSpace(bounds[0]))
			if err != nil {
				return nil, fmt.Errorf("invalid CPU number: %s", bounds[0])
			}

			end, err := strconv.Atoi(strings.TrimSpace(bounds[1]))
			if err != nil {
				return nil, fmt.Errorf("invalid CPU number: %s", bounds[1])
			}

			if start > end {
				return nil, fmt.Errorf("invalid CPU range: start %d > end %d", start, end)
			}

			for i := start; i <= end; i++ {
				cpuIDs = append(cpuIDs, i)
			}
		} else {
			cpuID, err := strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("invalid CPU number: %s", part)
			}
			cpuIDs = append(cpuIDs, cpuID)
		}
	}

	return cpuIDs, nil
}
