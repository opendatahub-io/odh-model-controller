package processors

import (
	"github.com/opendatahub-io/odh-model-controller/controllers/comparators"
	"github.com/opendatahub-io/odh-model-controller/controllers/utils"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type DeltaProcessor interface {
	ComputeDelta(comparator comparators.ResourceComparator, requestedResource client.Object, deployedResource client.Object) ResourceDelta
}

type deltaProcessor struct {
}

func NewDeltaProcessor() DeltaProcessor {
	return &deltaProcessor{}
}

func (d *deltaProcessor) ComputeDelta(comparator comparators.ResourceComparator, requestedResource client.Object, deployedResource client.Object) ResourceDelta {
	var added bool
	var updated bool
	var removed bool

	if utils.IsNotNil(requestedResource) && utils.IsNil(deployedResource) {
		added = true
	} else if utils.IsNil(requestedResource) && utils.IsNotNil(deployedResource) {
		removed = true
	} else if !comparator.Compare(deployedResource, requestedResource) {
		updated = true
	}

	return &resourceDelta{
		Added:   added,
		Updated: updated,
		Removed: removed,
	}
}

type ResourceDelta interface {
	HasChanges() bool
	IsAdded() bool
	IsUpdated() bool
	IsRemoved() bool
}

type resourceDelta struct {
	Added   bool
	Updated bool
	Removed bool
}

func (delta *resourceDelta) HasChanges() bool {
	return delta.Added || delta.Updated || delta.Removed
}

func (delta *resourceDelta) IsAdded() bool {
	return delta.Added
}

func (delta *resourceDelta) IsUpdated() bool {
	return delta.Updated
}

func (delta *resourceDelta) IsRemoved() bool {
	return delta.Removed
}
