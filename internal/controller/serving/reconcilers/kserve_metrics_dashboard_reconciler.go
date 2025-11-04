/*

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package reconcilers

import (
	"context"
	"strconv"

	"github.com/go-logr/logr"
	"github.com/hashicorp/errwrap"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/comparators"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/processors"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Query struct {
	Title string `json:"title"`
	Query string `json:"query"`
}

type Config struct {
	Title   string  `json:"title"`
	Type    string  `json:"type"`
	Queries []Query `json:"queries"`
}

type MetricsDashboardConfigMapData struct {
	Data []Config `json:"data"`
}

var _ SubResourceReconciler = (*KserveMetricsDashboardReconciler)(nil)

type KserveMetricsDashboardReconciler struct {
	NoResourceRemoval
	client           client.Client
	configMapHandler resources.ConfigMapHandler
	deltaProcessor   processors.DeltaProcessor
}

func NewKserveMetricsDashboardReconciler(client client.Client) *KserveMetricsDashboardReconciler {
	return &KserveMetricsDashboardReconciler{
		client:           client,
		configMapHandler: resources.NewConfigMapHandler(client),
		deltaProcessor:   processors.NewDeltaProcessor(),
	}
}

func (r *KserveMetricsDashboardReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {

	// Create Desired resource
	desiredResource, err := r.createDesiredResource(ctx, log, isvc)
	if err != nil {
		return err
	}

	// Get Existing resource
	existingResource, err := r.getExistingResource(ctx, log, isvc)
	if err != nil {
		return err
	}

	// Process Delta
	if err = r.processDelta(ctx, log, desiredResource, existingResource); err != nil {
		return err
	}
	return nil
}

func (r *KserveMetricsDashboardReconciler) createDesiredResource(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) (*corev1.ConfigMap, error) {

	var err error
	var runtime *kservev1alpha1.ServingRuntime
	supported := false

	// there is the possibility to also have and nil model field, e.g:
	// predictor:
	//	containers:
	//		- name: kserve-container
	//		image: user/custom-model:v1
	if nil == isvc.Spec.Predictor.Model {
		log.V(1).Info("no `predictor.model` field found in InferenceService, no metrics will be available")
		return r.createConfigMap(isvc, false, log)
	}

	// resolve SR
	runtime, err = utils.FindSupportingRuntimeForISvc(ctx, r.client, log, isvc)
	if err != nil {
		if errwrap.Contains(err, constants.NoSuitableRuntimeError) {
			return r.createConfigMap(isvc, false, log)
		}
		log.Error(err, "Could not determine servingruntime for isvc")
		return nil, err
	}

	// supported is true only when a match on this map is found, is false otherwise
	data, supported := getMetricsData(runtime)
	configMap, err := r.createConfigMap(isvc, supported, log)
	if err != nil {
		return nil, err
	}
	if supported {
		finaldata := utils.SubstituteVariablesInQueries(data, isvc.Namespace, isvc.Name)
		configMap.Data["metrics"] = finaldata
	}

	return configMap, nil

}

func (r *KserveMetricsDashboardReconciler) createConfigMap(isvc *kservev1beta1.InferenceService, supported bool, log logr.Logger) (*corev1.ConfigMap, error) {
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      isvc.Name + constants.KserveMetricsConfigMapNameSuffix,
			Namespace: isvc.Namespace,
		},
		Data: map[string]string{
			"supported": strconv.FormatBool(supported),
		},
	}
	configMap.Labels = map[string]string{
		"opendatahub.io/managed":       "true",
		"app.kubernetes.io/name":       "odh-model-controller",
		"app.kubernetes.io/component":  "kserve",
		"app.kubernetes.io/part-of":    "odh-model-serving",
		"app.kubernetes.io/managed-by": "odh-model-controller",
	}
	if err := ctrl.SetControllerReference(isvc, configMap, r.client.Scheme()); err != nil {
		log.Error(err, "Unable to add OwnerReference to the Metrics Dashboard Configmap")
		return nil, err
	}
	return configMap, nil
}

func (r *KserveMetricsDashboardReconciler) getExistingResource(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) (*corev1.ConfigMap, error) {
	return r.configMapHandler.FetchConfigMap(ctx, log, types.NamespacedName{Name: isvc.Name + constants.KserveMetricsConfigMapNameSuffix, Namespace: isvc.Namespace})
}

func (r *KserveMetricsDashboardReconciler) processDelta(ctx context.Context, log logr.Logger, desiredResource *corev1.ConfigMap, existingResource *corev1.ConfigMap) (err error) {

	comparator := comparators.GetConfigMapComparator()
	delta := r.deltaProcessor.ComputeDelta(comparator, desiredResource, existingResource)
	if !delta.HasChanges() {
		log.V(1).Info("No delta found in metrics configmap")
		return nil
	}

	if delta.IsAdded() {
		log.V(1).Info("Delta found", "create", desiredResource.GetName())
		if err = r.client.Create(ctx, desiredResource); err != nil {
			return err
		}
	}
	if delta.IsUpdated() {
		log.V(1).Info("Delta found", "update", existingResource.GetName())
		rp := existingResource.DeepCopy()
		rp.Labels = desiredResource.Labels
		rp.Data = desiredResource.Data

		if err = r.client.Update(ctx, rp); err != nil {
			return err
		}
	}
	if delta.IsRemoved() {
		log.V(1).Info("Delta found", "delete", existingResource.GetName())
		if err = r.client.Delete(ctx, existingResource); err != nil {
			return err
		}
	}
	return nil
}

// getMetricsData determines the appropriate metrics configuration data based on the serving runtime annotations.
// It checks for specific runtime type annotations and returns the corresponding metrics data string.
//
// The function evaluates annotations in the following order:
//   - IsNimRuntimeAnnotation: Returns NIMMetricsData if set to "true"
//   - KServeRuntimeAnnotation with OvmsRuntimeName: Returns OvmsMetricsData
//   - KServeRuntimeAnnotation with VllmRuntimeName: Returns VllmMetricsData
//   - KServeRuntimeAnnotation with TgisRuntimeName: Returns TgisMetricsData
//
// Parameters:
//   - runtime: A pointer to the ServingRuntime object containing annotations to evaluate
//
// Returns:
//   - string: The metrics configuration data for the detected runtime type, or empty string if no match
//   - bool: True if a supported runtime type was detected, false otherwise
//
// Example usage:
//
//	metricsData, found := getMetricsData(servingRuntime)
//	if found {
//	    // Use metricsData for dashboard configuration
//	}
func getMetricsData(runtime *kservev1alpha1.ServingRuntime) (string, bool) {
	switch {
	case runtime.Annotations[utils.IsNimRuntimeAnnotation] == "true":
		return constants.NIMMetricsData, true
	case runtime.Spec.Annotations[constants.KServeRuntimeAnnotation] == constants.OvmsRuntimeName:
		return constants.OvmsMetricsData, true
	case runtime.Spec.Annotations[constants.KServeRuntimeAnnotation] == constants.VllmRuntimeName:
		return constants.VllmMetricsData, true
	case runtime.Spec.Annotations[constants.KServeRuntimeAnnotation] == constants.TgisRuntimeName:
		return constants.TgisMetricsData, true
	default:
		return "", false
	}
}
