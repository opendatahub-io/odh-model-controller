package informercache

import (
	"context"
	"fmt"
	"slices"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/metadata/metadatainformer"
	"k8s.io/client-go/rest"
	toolscache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	ctrlcache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var configMapGVR = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}

// ConfigMapNameSource watches one or more ConfigMaps using metadata-only
// informers and exact metadata.name field selectors.
type ConfigMapNameSource struct {
	client     metadata.Interface
	refs       []types.NamespacedName
	handler    handler.EventHandler
	predicates []predicate.Predicate

	mu        sync.Mutex
	started   bool
	ctx       context.Context
	queue     workqueue.TypedRateLimitingInterface[reconcile.Request]
	informers map[types.NamespacedName]*managedInformer
}

type managedInformer struct {
	informer toolscache.SharedIndexInformer
	cancel   context.CancelFunc
}

// NewConfigMapNameSource creates a metadata-only source for exact ConfigMap
// names. An empty namespace means all namespaces.
func NewConfigMapNameSource(
	config *rest.Config,
	refs []types.NamespacedName,
	eventHandler handler.EventHandler,
	predicates ...predicate.Predicate,
) (*ConfigMapNameSource, error) {
	metadataClient, err := metadata.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create metadata client: %w", err)
	}

	return &ConfigMapNameSource{
		client:     metadataClient,
		refs:       uniqueReferences(refs),
		handler:    eventHandler,
		predicates: predicates,
		informers:  make(map[types.NamespacedName]*managedInformer),
	}, nil
}

func (s *ConfigMapNameSource) Start(ctx context.Context, queue workqueue.TypedRateLimitingInterface[reconcile.Request]) error {
	s.mu.Lock()
	s.started = true
	s.ctx = ctx
	s.queue = queue
	refs := append([]types.NamespacedName(nil), s.refs...)
	s.mu.Unlock()

	for _, ref := range refs {
		if err := s.startReference(ref); err != nil {
			return err
		}
	}
	return nil
}

func (s *ConfigMapNameSource) WaitForSync(ctx context.Context) error {
	s.mu.Lock()
	informers := make([]toolscache.SharedIndexInformer, 0, len(s.informers))
	for _, managed := range s.informers {
		informers = append(informers, managed.informer)
	}
	s.mu.Unlock()

	for _, informer := range informers {
		if !toolscache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
			return fmt.Errorf("metadata ConfigMap informer did not sync")
		}
	}
	return nil
}

func (s *ConfigMapNameSource) String() string {
	return "metadata ConfigMap source"
}

func (s *ConfigMapNameSource) startReference(ref types.NamespacedName) error {
	s.mu.Lock()
	if _, exists := s.informers[ref]; exists {
		s.mu.Unlock()
		return nil
	}
	ctx := s.ctx
	queue := s.queue
	s.mu.Unlock()

	informer, err := newConfigMapInformer(s.client, ref)
	if err != nil {
		return err
	}
	if err := (&source.Informer{
		Informer:   informer,
		Handler:    s.handler,
		Predicates: s.predicates,
	}).Start(ctx, queue); err != nil {
		return err
	}

	informerCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	if _, exists := s.informers[ref]; exists {
		cancel()
		s.mu.Unlock()
		return nil
	}
	s.informers[ref] = &managedInformer{informer: informer, cancel: cancel}
	s.mu.Unlock()

	go informer.Run(informerCtx.Done())
	return nil
}

// ConfigMapReferenceSource maintains one exact metadata-only informer for each
// namespaced reference currently registered by an owner object.
type ConfigMapReferenceSource struct {
	client     metadata.Interface
	handler    handler.EventHandler
	predicates []predicate.Predicate

	mu         sync.Mutex
	started    bool
	ctx        context.Context
	queue      workqueue.TypedRateLimitingInterface[reconcile.Request]
	references map[types.NamespacedName]types.NamespacedName
	informers  map[types.NamespacedName]*managedInformer
}

// NewConfigMapReferenceSource creates a dynamic metadata-only ConfigMap
// source. Each owner can register one namespaced ConfigMap reference.
func NewConfigMapReferenceSource(
	config *rest.Config,
	eventHandler handler.EventHandler,
	predicates ...predicate.Predicate,
) (*ConfigMapReferenceSource, error) {
	metadataClient, err := metadata.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create metadata client: %w", err)
	}

	return &ConfigMapReferenceSource{
		client:     metadataClient,
		handler:    eventHandler,
		predicates: predicates,
		references: make(map[types.NamespacedName]types.NamespacedName),
		informers:  make(map[types.NamespacedName]*managedInformer),
	}, nil
}

func (s *ConfigMapReferenceSource) Start(ctx context.Context, queue workqueue.TypedRateLimitingInterface[reconcile.Request]) error {
	s.mu.Lock()
	s.started = true
	s.ctx = ctx
	s.queue = queue
	refs := s.currentReferencesLocked()
	s.mu.Unlock()

	for _, ref := range refs {
		if err := s.startReference(ref); err != nil {
			return err
		}
	}
	return nil
}

func (s *ConfigMapReferenceSource) WaitForSync(ctx context.Context) error {
	// References can change during startup. Only wait for informers that are
	// still registered; removed informers have been cancelled and cannot sync.
	if !toolscache.WaitForCacheSync(ctx.Done(), func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, managed := range s.informers {
			if !managed.informer.HasSynced() {
				return false
			}
		}
		return true
	}) {
		return fmt.Errorf("metadata ConfigMap informer did not sync")
	}
	return nil
}

func (s *ConfigMapReferenceSource) String() string {
	return "dynamic metadata ConfigMap source"
}

// SetReference registers or updates an owner's ConfigMap reference. Passing
// nil removes the owner's reference.
func (s *ConfigMapReferenceSource) SetReference(owner types.NamespacedName, ref *types.NamespacedName) error {
	s.mu.Lock()
	if ref == nil {
		delete(s.references, owner)
	} else {
		s.references[owner] = *ref
	}
	refs := s.currentReferencesLocked()
	started := s.started
	s.mu.Unlock()

	if !started {
		return nil
	}

	for _, reference := range refs {
		if err := s.startReference(reference); err != nil {
			return err
		}
	}

	return s.stopUnreferenced(refs)
}

func (s *ConfigMapReferenceSource) currentReferencesLocked() []types.NamespacedName {
	refs := make(map[types.NamespacedName]struct{}, len(s.references))
	for _, ref := range s.references {
		refs[ref] = struct{}{}
	}

	result := make([]types.NamespacedName, 0, len(refs))
	for ref := range refs {
		result = append(result, ref)
	}
	return result
}

func (s *ConfigMapReferenceSource) startReference(ref types.NamespacedName) error {
	s.mu.Lock()
	if _, exists := s.informers[ref]; exists || !s.started {
		s.mu.Unlock()
		return nil
	}
	ctx := s.ctx
	queue := s.queue
	s.mu.Unlock()

	informer, err := newConfigMapInformer(s.client, ref)
	if err != nil {
		return err
	}
	if err := (&source.Informer{
		Informer:   informer,
		Handler:    s.handler,
		Predicates: s.predicates,
	}).Start(ctx, queue); err != nil {
		return err
	}

	informerCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	if _, exists := s.informers[ref]; exists || !slices.Contains(s.currentReferencesLocked(), ref) {
		cancel()
		s.mu.Unlock()
		return nil
	}
	s.informers[ref] = &managedInformer{informer: informer, cancel: cancel}
	s.mu.Unlock()

	go informer.Run(informerCtx.Done())
	return nil
}

func (s *ConfigMapReferenceSource) stopUnreferenced(refs []types.NamespacedName) error {
	keep := make(map[types.NamespacedName]struct{}, len(refs))
	for _, ref := range refs {
		keep[ref] = struct{}{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for ref, informer := range s.informers {
		if _, exists := keep[ref]; exists {
			continue
		}
		informer.cancel()
		delete(s.informers, ref)
	}
	return nil
}

func newConfigMapInformer(client metadata.Interface, ref types.NamespacedName) (toolscache.SharedIndexInformer, error) {
	namespace := ref.Namespace
	if namespace == "" {
		namespace = metav1.NamespaceAll
	}

	informer := metadatainformer.NewFilteredMetadataInformer(
		client,
		configMapGVR,
		namespace,
		0,
		toolscache.Indexers{toolscache.NamespaceIndex: toolscache.MetaNamespaceIndexFunc},
		func(options *metav1.ListOptions) {
			options.FieldSelector = fields.OneTermEqualSelector("metadata.name", ref.Name).String()
		},
	)
	if err := informer.Informer().SetTransform(StripConfigMapData); err != nil {
		return nil, fmt.Errorf("set ConfigMap metadata transform: %w", err)
	}
	return informer.Informer(), nil
}

func uniqueReferences(refs []types.NamespacedName) []types.NamespacedName {
	seen := make(map[types.NamespacedName]struct{}, len(refs))
	result := make([]types.NamespacedName, 0, len(refs))
	for _, ref := range refs {
		if ref.Name == "" {
			continue
		}
		if _, exists := seen[ref]; exists {
			continue
		}
		seen[ref] = struct{}{}
		result = append(result, ref)
	}
	return result
}

var _ source.SyncingSource = (*ConfigMapNameSource)(nil)
var _ source.SyncingSource = (*ConfigMapReferenceSource)(nil)
var _ ctrlcache.Informer = (toolscache.SharedIndexInformer)(nil)
