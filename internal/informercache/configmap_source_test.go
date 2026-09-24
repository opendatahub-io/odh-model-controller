package informercache

import (
	"context"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	toolscache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestConfigMapNameSourceDeduplicatesReferences(t *testing.T) {
	source, err := NewConfigMapNameSource(
		&rest.Config{Host: "https://example.invalid"},
		[]types.NamespacedName{
			{Name: "config", Namespace: "namespace"},
			{Name: "config", Namespace: "namespace"},
			{Name: "other", Namespace: "namespace"},
			{Name: "ignored", Namespace: "namespace"},
			{},
		},
		handler.EnqueueRequestsFromMapFunc(func(_ context.Context, _ client.Object) []reconcile.Request {
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("NewConfigMapNameSource() error = %v", err)
	}

	if len(source.refs) != 3 {
		t.Fatalf("got %d references, want 3", len(source.refs))
	}
	if source.refs[0] != (types.NamespacedName{Name: "config", Namespace: "namespace"}) {
		t.Fatalf("unexpected first reference: %#v", source.refs[0])
	}
}

func TestConfigMapReferenceSourceTracksOwnerReferences(t *testing.T) {
	source, err := NewConfigMapReferenceSource(
		&rest.Config{Host: "https://example.invalid"},
		handler.EnqueueRequestsFromMapFunc(func(_ context.Context, _ client.Object) []reconcile.Request {
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("NewConfigMapReferenceSource() error = %v", err)
	}

	owner := types.NamespacedName{Name: "account", Namespace: "namespace"}
	ref := types.NamespacedName{Name: "model-list", Namespace: "namespace"}
	if err := source.SetReference(owner, &ref); err != nil {
		t.Fatalf("SetReference() error = %v", err)
	}

	got := source.currentReferencesLocked()
	if len(got) != 1 || got[0] != ref {
		t.Fatalf("references = %#v, want %#v", got, []types.NamespacedName{ref})
	}

	if err := source.SetReference(owner, nil); err != nil {
		t.Fatalf("SetReference(nil) error = %v", err)
	}
	if got := source.currentReferencesLocked(); len(got) != 0 {
		t.Fatalf("references after removal = %#v, want empty", got)
	}
}

type pendingInformer struct {
	toolscache.SharedIndexInformer
	checked chan struct{}
}

func (i *pendingInformer) HasSynced() bool {
	select {
	case i.checked <- struct{}{}:
	default:
	}
	return false
}

func TestConfigMapReferenceSourceRemovedReferenceDuringSync(t *testing.T) {
	owner := types.NamespacedName{Namespace: "ns", Name: "account"}
	ref := types.NamespacedName{Namespace: "ns", Name: "models"}
	informer := &pendingInformer{checked: make(chan struct{}, 1)}
	s := &ConfigMapReferenceSource{
		started:    true,
		references: map[types.NamespacedName]types.NamespacedName{owner: ref},
		informers:  map[types.NamespacedName]*managedInformer{ref: {informer: informer, cancel: func() {}}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- s.WaitForSync(ctx) }()
	select {
	case <-informer.checked:
	case <-ctx.Done():
		t.Fatal("timed out waiting for cache synchronization to start")
	}
	if err := s.SetReference(owner, nil); err != nil {
		t.Fatal(err)
	}
	if len(s.informers) != 0 {
		t.Fatal("reference not removed")
	}
	if err := <-result; err != nil {
		t.Fatalf("source has no remaining references, but startup still fails: %v", err)
	}
}

func TestConfigMapReferenceSourceDoesNotRestartRemovedReference(t *testing.T) {
	s, err := NewConfigMapReferenceSource(&rest.Config{Host: "https://example.invalid"}, &handler.EnqueueRequestForObject{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
	defer queue.ShutDown()
	if err := s.Start(ctx, queue); err != nil {
		t.Fatal(err)
	}
	// Simulate Start resuming with a reference removed since its snapshot.
	ref := types.NamespacedName{Namespace: "ns", Name: "removed"}
	if err := s.startReference(ref); err != nil {
		t.Fatal(err)
	}
	if len(s.informers) != 0 {
		t.Fatal("started an informer for a reference that is no longer registered")
	}
}
