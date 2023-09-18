package components

import (
	"context"
	"github.com/go-logr/logr"
	"github.com/opendatahub-io/odh-model-controller/controllers/comparators"
	v1 "github.com/openshift/api/route/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type RouteHandler interface {
	FetchRoute(key types.NamespacedName) (*v1.Route, error)
	GetComparator() comparators.ResourceComparator
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

func (o *routeHandler) FetchRoute(key types.NamespacedName) (*v1.Route, error) {
	route := &v1.Route{}
	err := o.Get(o.ctx, key, route)
	if err != nil && errors.IsNotFound(err) {
		o.log.Info("Openshift Route not found.")
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	o.log.Info("Successfully fetch deployed Openshift Route")
	return route, nil
}

func (o *routeHandler) GetComparator() comparators.ResourceComparator {
	return &comparators.RouteComparator{}
}
