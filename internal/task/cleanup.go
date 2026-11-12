package task

import (
	"context"
	"fmt"
	"log"
	"sync"

	k8sclient "k8s.io/client-go/kubernetes"

	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/kubernetes"
)

// trackedRef identifies one object this run created. Cleanup deletes exactly
// these refs — no name prefixes, no namespace sweeps.
type trackedRef struct {
	kind      string // "pod" or "secret"
	namespace string
	name      string
}

// objectTracker records every cluster object a task creates so the gated
// exit-path cleanup can delete exactly those objects — nothing it did not
// create. Safe for concurrent use (per-node-data creates pods in parallel).
type objectTracker struct {
	mu      sync.Mutex
	objects []trackedRef
}

// Add records a created object. Call right after a successful create.
func (t *objectTracker) Add(kind, namespace, name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.objects = append(t.objects, trackedRef{kind: kind, namespace: namespace, name: name})
}

// DeleteAll deletes every tracked object. Errors are logged, not returned —
// the task's primary result takes precedence over cleanup failures.
// WithoutCancel keeps deletion working when the run was aborted mid-flight.
func (t *objectTracker) DeleteAll(ctx context.Context, client k8sclient.Interface, taskName string) {
	cleanupCtx := context.WithoutCancel(ctx)
	t.mu.Lock()
	objects := t.objects
	t.objects = nil
	t.mu.Unlock()
	for _, obj := range objects {
		var err error
		switch obj.kind {
		case "pod":
			err = kubernetes.DeletePod(cleanupCtx, client, obj.namespace, obj.name)
		case "secret":
			err = kubernetes.DeleteSecret(cleanupCtx, client, obj.namespace, obj.name)
		default:
			err = fmt.Errorf("unknown tracked kind %q", obj.kind)
		}
		if err != nil {
			log.Printf("%s: %v", taskName, err)
		}
	}
}
