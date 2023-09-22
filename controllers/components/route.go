package components

import (
	"context"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/opendatahub-io/odh-model-controller/controllers/comparators"
	v1 "github.com/openshift/api/route/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// RouteHandler to provide route specific implementation.
type RouteHandler interface {
	FetchRoute(key types.NamespacedName) (*v1.Route, error)
	GetComparator() comparators.ResourceComparator
	DeleteRoute(key types.NamespacedName) error
}

type routeHandler struct {
	client.Client
	ctx context.Context
	log logr.Logger
}

func NewRouteHandler(client client.Client, ctx context.Context, log logr.Logger) RouteHandler {
	return &routeHandler{
		Client: client,
		ctx:    ctx,
		log:    log,
	}
}

func (r *routeHandler) FetchRoute(key types.NamespacedName) (*v1.Route, error) {
	route := &v1.Route{}
	err := r.Get(r.ctx, key, route)
	if err != nil && errors.IsNotFound(err) {
		r.log.Info("Openshift Route not found.")
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	r.log.Info("Successfully fetch deployed Openshift Route")
	return route, nil
}

func (r *routeHandler) GetComparator() comparators.ResourceComparator {
	return &comparators.RouteComparator{}
}

func (r *routeHandler) DeleteRoute(key types.NamespacedName) error {
	route := &v1.Route{}
	err := r.Get(r.ctx, key, route)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if err = r.Delete(r.ctx, route); err != nil {
		return fmt.Errorf("failed to delete route: %w", err)
	}

	return nil
}