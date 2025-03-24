package reconcilers

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	infrctrl "github.com/kubeflow/model-registry/pkg/inferenceservice-controller"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

var (
	errGetDSC                = errors.New("failed to get DataScienceCluster")
	errGetMRNamespaceFromDSC = errors.New("failed to get Model Registry Namespace from DataScienceCluster")
)

func NewModelRegistryInferenceServiceReconciler(client client.Client, log logr.Logger, skipTLSVerify bool, bearerToken string) (*infrctrl.InferenceServiceController, error) {
	dsc := unstructured.Unstructured{}

	dscList := unstructured.UnstructuredList{}

	dscList.SetGroupVersionKind(utils.GVK.DataScienceCluster)

	err := client.List(context.Background(), &dscList)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errGetDSC, err)
	}

	if len(dscList.Items) == 0 || len(dscList.Items) > 1 {
		return nil, fmt.Errorf("%w: only one DataScienceCluster is allowed", errGetDSC)
	}

	dsc = dscList.Items[0]

	ns, found, err := unstructured.NestedFieldCopy(dsc.Object, "spec", "components", "modelregistry", "registriesNamespace")
	if err != nil || !found {
		return nil, fmt.Errorf("%w: %w", errGetMRNamespaceFromDSC, err)
	}

	mrNamespaceFromDSC, isNsOk := ns.(string)
	if !isNsOk || mrNamespaceFromDSC == "" {
		return nil, fmt.Errorf("%w: invalid namespace", errGetMRNamespaceFromDSC)
	}

	log.Info("Model Registry Namespace from DataScienceCluster", "Namespace", mrNamespaceFromDSC)

	return infrctrl.NewInferenceServiceController(
		client,
		log,
		skipTLSVerify,
		bearerToken,
		constants.ModelRegistryInferenceServiceIdLabel,
		constants.ModelRegistryRegisteredModelIdLabel,
		constants.ModelRegistryModelVersionIdLabel,
		constants.ModelRegistryNamespaceLabel,
		constants.ModelRegistryNameLabel,
		constants.ModelRegistryUrlAnnotation,
		constants.ModelRegistryFinalizer,
		constants.ModelRegistryServiceAnnotation,
		mrNamespaceFromDSC,
	), nil
}
