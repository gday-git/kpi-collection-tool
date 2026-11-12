package kubernetes

import "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	// ManagedByLabel marks every object the tool creates, so leftovers from a
	// killed or kept run can be identified and removed safely. The pre-run
	// sweep and all ownership decisions select on this label alone.
	ManagedByLabel = "app.kubernetes.io/managed-by"
	// ManagedByValue is the selector value for ManagedByLabel.
	ManagedByValue = "kpi-collector"
	// RunIDLabel records which run created the object, for diagnostics only.
	// Deliberately not used as a deletion filter: any kpi-collector run may
	// sweep another run's leftovers (there is no run lock yet).
	RunIDLabel = "kpi-collector.io/run-id"
	// ManagedBySelector lists every object created by this tool.
	ManagedBySelector = ManagedByLabel + "=" + ManagedByValue
)

// LabelObject stamps the ownership labels onto meta, merging with — never
// clobbering — labels already present (user-supplied pod specs may carry
// their own).
func LabelObject(meta *v1.ObjectMeta, runID string) {
	if meta.Labels == nil {
		meta.Labels = map[string]string{}
	}
	meta.Labels[ManagedByLabel] = ManagedByValue
	meta.Labels[RunIDLabel] = runID
}
