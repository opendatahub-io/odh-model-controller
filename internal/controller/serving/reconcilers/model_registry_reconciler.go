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

// mrNamespaceFields lists the DataScienceCluster paths that may hold the Model Registry
// namespace, in the order they are probed. DataScienceCluster v3 folded Model Registry into the
// AI Hub component and renamed the field; v1 and v2 use the legacy path. The operator converts
// between the two, so only the field belonging to the served version is present. Probing by field
// rather than by version number keeps this working across future API versions.
var mrNamespaceFields = [][]string{
	{"spec", "components", "aiHub", "instancesNamespace"},
	{"spec", "components", "modelregistry", "registriesNamespace"},
}

// modelRegistryNamespace reads the Model Registry namespace from a DataScienceCluster of any
// served version.
func modelRegistryNamespace(dsc *unstructured.Unstructured) (string, error) {
	for _, fields := range mrNamespaceFields {
		namespace, found, err := unstructured.NestedString(dsc.Object, fields...)
		if err != nil {
			return "", fmt.Errorf("%w: %w", errGetMRNamespaceFromDSC, err)
		}
		if found && namespace != "" {
			return namespace, nil
		}
	}

	return "", fmt.Errorf("%w: invalid namespace", errGetMRNamespaceFromDSC)
}

func NewModelRegistryInferenceServiceReconciler(client client.Client, log logr.Logger, skipTLSVerify bool, bearerToken string) (*infrctrl.InferenceServiceController, error) {
	dscList, err := utils.ListUnstructuredByGroupKind(context.Background(), client, utils.GroupKind.DataScienceCluster)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errGetDSC, err)
	}

	if len(dscList.Items) != 1 {
		return nil, fmt.Errorf("%w: only one DataScienceCluster is allowed", errGetDSC)
	}

	mrNamespaceFromDSC, err := modelRegistryNamespace(&dscList.Items[0])
	if err != nil {
		return nil, err
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
