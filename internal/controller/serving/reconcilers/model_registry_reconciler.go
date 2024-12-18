package reconcilers

import (
	"context"

	"github.com/go-logr/logr"
	infrctrl "github.com/kubeflow/model-registry/pkg/inferenceservice-controller"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

func NewModelRegistryInferenceServiceReconciler(client client.Client, log logr.Logger, skipTLSVerify bool, bearerToken string) *infrctrl.InferenceServiceController {
	mrNamespaceFromDSC := ""

	dsc := unstructured.Unstructured{}

	dscList := unstructured.UnstructuredList{}

	dscList.SetGroupVersionKind(utils.GVK.DataScienceCluster)

	err := client.List(context.Background(), &dscList)
	if err != nil {
		log.Error(err, "Failed to list DataScienceCluster")
	}

	if len(dscList.Items) > 0 {
		dsc = dscList.Items[0]

		ns, found, err := unstructured.NestedFieldCopy(dsc.Object, "spec", "components", "modelRegistry", "registriesNamespace")
		if err == nil && found {
			mrNamespaceFromDSC = ns.(string)
		}
	}

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
		mrNamespaceFromDSC,
	)
}
