package reconcilers

import (
	"context"

	"github.com/go-logr/logr"
	mrapi "github.com/opendatahub-io/model-registry/pkg/api"
	"github.com/opendatahub-io/model-registry/pkg/openapi"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ModelRegistryServingEnvironmentReconciler struct {
	client client.Client
}

func NewModelRegistryServingEnvironmentReconciler(client client.Client) *ModelRegistryServingEnvironmentReconciler {
	return &ModelRegistryServingEnvironmentReconciler{
		client: client,
	}
}

func (r *ModelRegistryServingEnvironmentReconciler) Reconcile(ctx context.Context, log logr.Logger, mrClient mrapi.ModelRegistryApi, namespace string) error {
	// Fetch the ServingEnvironment corresponding to the current ServingRuntime CR
	_, err := mrClient.GetServingEnvironmentByParams(&namespace, nil)
	if err != nil {
		// Create new ServingEnvironment as not already existing
		// TODO: we could fetch additional custom props from the ServingRuntime CR, needed?
		log.Info("Creating new ServingEnvironment for " + namespace)
		_, err = mrClient.UpsertServingEnvironment(&openapi.ServingEnvironment{
			Name:       &namespace,
			ExternalID: &namespace,
		})
		if err != nil {
			return err
		}
	}

	return nil
}
