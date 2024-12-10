package serving

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

func ApplyDefaultServerlessAnnotations(ctx context.Context, client client.Client, resourceName string, resourceMetadata *v1.ObjectMeta, logger logr.Logger) error {
	deploymentMode, err := utils.GetDeploymentModeForKServeResource(ctx, client, resourceMetadata.GetAnnotations())
	if err != nil {
		return fmt.Errorf("error resolving deployment mode for resource %s: %w", resourceName, err)
	}

	if deploymentMode == constants.Serverless {
		logAnnotationsAdded := make([]string, 0, 3)
		resourceAnnotations := resourceMetadata.GetAnnotations()
		if resourceAnnotations == nil {
			resourceAnnotations = make(map[string]string)
		}

		if _, exists := resourceAnnotations["serving.knative.openshift.io/enablePassthrough"]; !exists {
			resourceAnnotations["serving.knative.openshift.io/enablePassthrough"] = "true"
			logAnnotationsAdded = append(logAnnotationsAdded, "serving.knative.openshift.io/enablePassthrough")
		}

		if _, exists := resourceAnnotations["sidecar.istio.io/inject"]; !exists {
			resourceAnnotations["sidecar.istio.io/inject"] = "true"
			logAnnotationsAdded = append(logAnnotationsAdded, "sidecar.istio.io/inject")
		}

		if _, exists := resourceAnnotations["sidecar.istio.io/rewriteAppHTTPProbers"]; !exists {
			resourceAnnotations["sidecar.istio.io/rewriteAppHTTPProbers"] = "true"
			logAnnotationsAdded = append(logAnnotationsAdded, "sidecar.istio.io/rewriteAppHTTPProbers")
		}

		if len(logAnnotationsAdded) > 0 {
			logger.V(1).Info("Annotations added", "annotations", strings.Join(logAnnotationsAdded, ","))
		}

		resourceMetadata.SetAnnotations(resourceAnnotations)
	}
	return nil
}
