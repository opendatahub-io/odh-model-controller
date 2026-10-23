package reconcilers

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	infrctrl "github.com/kubeflow/hub/pkg/inferenceservice-controller"
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
	dscList, err := utils.ListUnstructuredByGroupKind(context.Background(), client, utils.GroupKind.DataScienceCluster)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errGetDSC, err)
	}

	if len(dscList.Items) == 0 || len(dscList.Items) > 1 {
		return nil, fmt.Errorf("%w: only one DataScienceCluster is allowed", errGetDSC)
	}

	dsc := dscList.Items[0]

	// DSC v3 exposes Model Registry through AI Hub; v1 and v2 use the legacy field.
	component, namespaceField := "aiHub", "instancesNamespace"
	if dscList.GroupVersionKind().Version == "v1" || dscList.GroupVersionKind().Version == "v2" {
		component, namespaceField = "modelregistry", "registriesNamespace"
	}

	mrNamespaceFromDSC, found, err := unstructured.NestedString(dsc.Object, "spec", "components", component, namespaceField)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errGetMRNamespaceFromDSC, err)
	}
	if !found || mrNamespaceFromDSC == "" {
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
