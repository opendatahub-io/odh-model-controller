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

func TestConfigMapReferenceSourceStopsOnlyUnreferencedInformers(t *testing.T) {
	firstOwner := types.NamespacedName{Namespace: "ns", Name: "account-a"}
	secondOwner := types.NamespacedName{Namespace: "ns", Name: "account-b"}
	sharedRef := types.NamespacedName{Namespace: "ns", Name: "models"}
	staleRef := types.NamespacedName{Namespace: "ns", Name: "old-models"}
	sharedStopped := false
	staleStopped := false

	source := &ConfigMapReferenceSource{
		references: map[types.NamespacedName]types.NamespacedName{
			firstOwner:  sharedRef,
			secondOwner: sharedRef,
		},
		informers: map[types.NamespacedName]*managedInformer{
			sharedRef: {cancel: func() { sharedStopped = true }},
			staleRef:  {cancel: func() { staleStopped = true }},
		},
	}

	if err := source.stopUnreferenced(); err != nil {
		t.Fatalf("stopUnreferenced() error = %v", err)
	}
	if sharedStopped {
		t.Fatal("stopped an informer still referenced by Accounts")
	}
	if !staleStopped {
		t.Fatal("did not stop an informer with no live references")
	}
	if len(source.informers) != 1 {
		t.Fatalf("got %d informers, want only the shared referenced informer", len(source.informers))
	}
	if _, exists := source.informers[sharedRef]; !exists {
		t.Fatalf("live informer %v was removed", sharedRef)
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
	source := &ConfigMapReferenceSource{
		started:    true,
		references: map[types.NamespacedName]types.NamespacedName{owner: ref},
		informers: map[types.NamespacedName]*managedInformer{
			ref: {informer: informer, cancel: func() {}},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- source.WaitForSync(ctx)
	}()

	select {
	case <-informer.checked:
	case <-ctx.Done():
		t.Fatal("timed out waiting for cache synchronization to start")
	}

	if err := source.SetReference(owner, nil); err != nil {
		t.Fatalf("SetReference(nil) error = %v", err)
	}

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("startup failed after the only reference was removed: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("WaitForSync continued waiting on the removed informer")
	}
}

func TestConfigMapReferenceSourceDoesNotStartRemovedReference(t *testing.T) {
	source, err := NewConfigMapReferenceSource(
		&rest.Config{Host: "https://example.invalid"},
		handler.EnqueueRequestsFromMapFunc(func(_ context.Context, _ client.Object) []reconcile.Request {
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("NewConfigMapReferenceSource() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
	defer queue.ShutDown()

	owner := types.NamespacedName{Namespace: "ns", Name: "account"}
	ref := types.NamespacedName{Namespace: "ns", Name: "models"}
	source.mu.Lock()
	source.started = true
	source.ctx = ctx
	source.queue = queue
	source.references[owner] = ref
	staleSnapshot := source.currentReferencesLocked()
	source.mu.Unlock()

	if err := source.SetReference(owner, nil); err != nil {
		t.Fatalf("SetReference(nil) error = %v", err)
	}
	if len(staleSnapshot) != 1 || staleSnapshot[0] != ref {
		t.Fatalf("stale snapshot = %#v, want [%#v]", staleSnapshot, ref)
	}

	// Simulate Start processing a reference from its snapshot after an Account
	// event has already removed that reference.
	if err := source.startReference(staleSnapshot[0]); err != nil {
		t.Fatalf("startReference() error = %v", err)
	}

	source.mu.Lock()
	defer source.mu.Unlock()
	if len(source.informers) != 0 {
		t.Fatalf("started informers for removed references: %#v", source.informers)
	}
}

func TestConfigMapReferenceSourceDoesNotStartRetargetedReference(t *testing.T) {
	source, err := NewConfigMapReferenceSource(
		&rest.Config{Host: "https://example.invalid"},
		handler.EnqueueRequestsFromMapFunc(func(_ context.Context, _ client.Object) []reconcile.Request {
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("NewConfigMapReferenceSource() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
	defer queue.ShutDown()

	owner := types.NamespacedName{Namespace: "ns", Name: "account"}
	oldRef := types.NamespacedName{Namespace: "ns", Name: "old-models"}
	newRef := types.NamespacedName{Namespace: "ns", Name: "new-models"}
	source.mu.Lock()
	source.started = true
	source.ctx = ctx
	source.queue = queue
	source.references[owner] = oldRef
	staleSnapshot := source.currentReferencesLocked()
	// Simulate an Account update retargeting the reference after Start took
	// its snapshot but before it started the old informer.
	source.references[owner] = newRef
	source.mu.Unlock()

	if len(staleSnapshot) != 1 || staleSnapshot[0] != oldRef {
		t.Fatalf("stale snapshot = %#v, want [%#v]", staleSnapshot, oldRef)
	}
	if err := source.startReference(staleSnapshot[0]); err != nil {
		t.Fatalf("startReference() error = %v", err)
	}

	source.mu.Lock()
	defer source.mu.Unlock()
	if len(source.informers) != 0 {
		t.Fatalf("started informers for a retargeted reference: %#v", source.informers)
	}
}
