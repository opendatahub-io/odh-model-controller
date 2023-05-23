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

package controllers

import (
	"context"
	"fmt"
	"reflect"
	"strconv"

	inferenceservicev1 "github.com/kserve/modelmesh-serving/apis/serving/v1beta1"
	"istio.io/api/meta/v1alpha1"
	"istio.io/api/networking/v1alpha3"
	virtualservicev1 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	v1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NewInferenceServiceVirtualService defines the desired VirtualService object
func NewInferenceServiceVirtualService(inferenceservice *inferenceservicev1.InferenceService) *virtualservicev1.VirtualService {
	grpcRoute := &v1alpha3.HTTPRoute{
		Match: []*v1alpha3.HTTPMatchRequest{{
			Method: &v1alpha3.StringMatch{MatchType: &v1alpha3.StringMatch_Exact{Exact: "POST"}},
			Headers: map[string]*v1alpha3.StringMatch{
				"content-type": {MatchType: &v1alpha3.StringMatch_Prefix{Prefix: "application/grpc"}},
				"mm-vmodel-id": {MatchType: &v1alpha3.StringMatch_Exact{Exact: inferenceservice.Name}},
			},
		}},
		Route: []*v1alpha3.HTTPRouteDestination{
			{
				Destination: &v1alpha3.Destination{
					Host: internalModelMeshFQDN(inferenceservice.Namespace),
					Port: &v1alpha3.PortSelector{
						Number: 8033,
					},
				},
			},
		},
	}

	httpRoute := &v1alpha3.HTTPRoute{
		Match: []*v1alpha3.HTTPMatchRequest{{
			Uri: &v1alpha3.StringMatch{
				MatchType: &v1alpha3.StringMatch_Exact{
					Exact: "/modelmesh/" + inferenceservice.Namespace + "/v2/models/" + inferenceservice.Name + "/infer",
				},
			},
		}},
		Rewrite: &v1alpha3.HTTPRewrite{
			Uri: "/v2/models/" + inferenceservice.Name + "/infer",
		},
		Route: []*v1alpha3.HTTPRouteDestination{{
			Destination: &v1alpha3.Destination{
				Host: internalModelMeshFQDN(inferenceservice.Namespace),
				Port: &v1alpha3.PortSelector{
					Number: 8008,
				},
			},
		}},
	}

	return &virtualservicev1.VirtualService{
		TypeMeta:   metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{Name: inferenceservice.Name, Namespace: inferenceservice.Namespace, Labels: map[string]string{"inferenceservice-name": inferenceservice.Name}},
		Spec: v1alpha3.VirtualService{
			Hosts: []string{"*"},
			Http:  []*v1alpha3.HTTPRoute{grpcRoute, httpRoute},
		},
		Status: v1alpha1.IstioStatus{},
	}
}

func buildTrafficSplittingGrpcRoute(modelTag string, validISvcs []inferenceservicev1.InferenceService) (*v1alpha3.HTTPRoute, error) {
	servingNamespace := validISvcs[0].Namespace

	grpcDestinations := make([]*v1alpha3.HTTPRouteDestination, len(validISvcs), len(validISvcs))
	for idx, isvc := range validISvcs {
		grpcDestination := v1alpha3.HTTPRouteDestination{
			Destination: &v1alpha3.Destination{
				Host: internalModelMeshFQDN(servingNamespace),
				Port: &v1alpha3.PortSelector{
					Number: 8033,
				},
			},
			Headers: &v1alpha3.Headers{
				Request: &v1alpha3.Headers_HeaderOperations{
					Set: map[string]string{"mm-vmodel-id": isvc.Name},
				},
			},
		}

		if percentStr, ok := isvc.Annotations[InferenceServiceSplitPercentAnnotation]; ok {
			percent, parseErr := strconv.ParseInt(percentStr, 10, 32)
			if parseErr != nil {
				return nil, parseErr
			}
			grpcDestination.Weight = int32(percent)
		}

		grpcDestinations[idx] = &grpcDestination
	}

	return &v1alpha3.HTTPRoute{
		Name: "grpc-routing",
		Match: []*v1alpha3.HTTPMatchRequest{{
			Method: &v1alpha3.StringMatch{MatchType: &v1alpha3.StringMatch_Exact{Exact: "POST"}},
			Headers: map[string]*v1alpha3.StringMatch{
				"content-type": {MatchType: &v1alpha3.StringMatch_Prefix{Prefix: "application/grpc"}},
				"mm-vmodel-id": {MatchType: &v1alpha3.StringMatch_Exact{Exact: modelTag}},
			},
		}},
		Route: grpcDestinations,
	}, nil
}

func buildTrafficSplittingHttpRoute(modelTag string, validISvcs []inferenceservicev1.InferenceService) (*v1alpha3.HTTPRoute, error) {
	servingNamespace := validISvcs[0].Namespace

	splitRoutes := make([]*v1alpha3.HTTPRouteDestination, len(validISvcs), len(validISvcs))
	for idx, isvc := range validISvcs {
		split := v1alpha3.HTTPRouteDestination{
			Destination: &v1alpha3.Destination{
				Host: "istio-ingressgateway.istio-system.svc.cluster.local",
				Port: &v1alpha3.PortSelector{
					Number: 80,
				},
			},
			Headers: &v1alpha3.Headers{
				Request: &v1alpha3.Headers_HeaderOperations{
					Set: map[string]string{"x-vmodel": isvc.Name},
				},
			},
		}

		if percentStr, ok := isvc.Annotations[InferenceServiceSplitPercentAnnotation]; ok {
			percent, parseErr := strconv.ParseInt(percentStr, 10, 32)
			if parseErr != nil {
				return nil, parseErr
			}
			split.Weight = int32(percent)
		}

		splitRoutes[idx] = &split
	}

	return &v1alpha3.HTTPRoute{
		Match: []*v1alpha3.HTTPMatchRequest{{
			Uri: &v1alpha3.StringMatch{
				MatchType: &v1alpha3.StringMatch_Exact{
					Exact: "/modelmesh/" + servingNamespace + "/v2/models/" + modelTag + "/infer",
				},
			},
		}},
		Rewrite: &v1alpha3.HTTPRewrite{
			Uri: "/vmodel-route/" + servingNamespace + "/" + modelTag + "/infer",
		},
		Route: splitRoutes,
	}, nil
}

func buildHttpRedirectionRoutes(modelTag string, validISvcs []inferenceservicev1.InferenceService) []*v1alpha3.HTTPRoute {
	servingNamespace := validISvcs[0].Namespace

	splitRedirectionRoutes := make([]*v1alpha3.HTTPRoute, len(validISvcs), len(validISvcs))
	for idx, isvc := range validISvcs {
		redirection := &v1alpha3.HTTPRoute{
			Match: []*v1alpha3.HTTPMatchRequest{{
				Uri: &v1alpha3.StringMatch{
					MatchType: &v1alpha3.StringMatch_Exact{
						Exact: "/vmodel-route/" + servingNamespace + "/" + modelTag + "/infer",
					},
				},
				Headers: map[string]*v1alpha3.StringMatch{
					"x-vmodel": {MatchType: &v1alpha3.StringMatch_Exact{Exact: isvc.Name}},
				},
			}},
			Rewrite: &v1alpha3.HTTPRewrite{
				Uri: "/v2/models/" + isvc.Name + "/infer",
			},
			Route: []*v1alpha3.HTTPRouteDestination{{
				Destination: &v1alpha3.Destination{
					Host: internalModelMeshFQDN(servingNamespace),
					Port: &v1alpha3.PortSelector{
						Number: 8008,
					},
				},
			}},
		}

		splitRedirectionRoutes[idx] = redirection
	}

	return splitRedirectionRoutes
}

func buildTrafficSplittingVirtualService(modelTag string, validISvcs []inferenceservicev1.InferenceService) (*virtualservicev1.VirtualService, error) {
	grpcRoute, err := buildTrafficSplittingGrpcRoute(modelTag, validISvcs)
	if err != nil {
		return nil, err
	}

	httpSplitRoute, err := buildTrafficSplittingHttpRoute(modelTag, validISvcs)
	if err != nil {
		return nil, err
	}

	httpRedirectionRoutes := buildHttpRedirectionRoutes(modelTag, validISvcs)

	allVsRoutes := []*v1alpha3.HTTPRoute{grpcRoute, httpSplitRoute}
	allVsRoutes = append(allVsRoutes, httpRedirectionRoutes...)

	return &virtualservicev1.VirtualService{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      modelTag + "-splitting",
			Namespace: validISvcs[0].Namespace,
			Annotations: map[string]string{
				InferenceServiceModelTagLabel: modelTag,
			},
		},
		Spec: v1alpha3.VirtualService{
			Hosts: []string{"*"},
			Http:  allVsRoutes,
		},
		Status: v1alpha1.IstioStatus{},
	}, nil
}

// CompareInferenceServiceVirtualServices checks if two VirtualServices are equal, if not return false
func CompareInferenceServiceVirtualServices(vs1 *virtualservicev1.VirtualService, vs2 *virtualservicev1.VirtualService) bool {
	// Two VirtualServices will be equal if the labels and spec are identical
	return DeepCompare(&vs1.Labels, &vs2.Labels) && DeepCompare(&vs1.Spec, &vs2.Spec)
}

// DeepCompare compares only Exported field recursivly
func DeepCompare(a, b interface{}) bool {
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	if va.Kind() != vb.Kind() {
		return false
	}
	switch va.Kind() {
	case reflect.Struct:
		if va.Type() != vb.Type() {
			return false
		}
		for i := 0; i < va.NumField(); i++ {
			field := va.Type().Field(i)
			if field.PkgPath != "" { // field is unexported
				continue
			}
			if !DeepCompare(va.Field(i).Interface(), vb.Field(i).Interface()) {
				return false
			}
		}
		return true
	case reflect.Slice, reflect.Array:
		if va.Len() != vb.Len() {
			return false
		}
		for i := 0; i < va.Len(); i++ {
			if !DeepCompare(va.Index(i).Interface(), vb.Index(i).Interface()) {
				return false
			}
		}
		return true
	case reflect.Map:
		if va.Len() != vb.Len() {
			return false
		}
		keysA, keysB := va.MapKeys(), vb.MapKeys()
		for _, key := range keysA {
			if !DeepCompare(va.MapIndex(key).Interface(), vb.MapIndex(key).Interface()) {
				return false
			}
		}
		for _, key := range keysB {
			if !DeepCompare(va.MapIndex(key).Interface(), vb.MapIndex(key).Interface()) {
				return false
			}
		}
		return true
	case reflect.Ptr:
		if va.IsNil() != vb.IsNil() {
			return false
		}
		if va.IsNil() && vb.IsNil() {
			return true
		}
		return DeepCompare(va.Elem().Interface(), vb.Elem().Interface())
	default:
		return reflect.DeepEqual(a, b)
	}
}

func (r *OpenshiftInferenceServiceReconciler) updateTrafficSplitVirtualService(namespace *v1.Namespace, modelTag string, existentVs *virtualservicev1.VirtualService, ctx context.Context) (*virtualservicev1.VirtualService, error) {

	// Get list of InferenceServices tagged with `model-tag`
	taggedISvcs := &inferenceservicev1.InferenceServiceList{}
	err := r.List(ctx, taggedISvcs, client.InNamespace(namespace.Name), client.MatchingLabels{InferenceServiceModelTagLabel: modelTag})
	if err != nil {
		return nil, err
	}

	// Filter out ISVC that are being deleted
	var validISvcs []inferenceservicev1.InferenceService
	for _, isvc := range taggedISvcs.Items {
		if isvc.ObjectMeta.DeletionTimestamp.IsZero() {
			validISvcs = append(validISvcs, isvc)
		}
	}

	// If there are no non-deleted ISVCs, delete the VirtualService for traffic splitting
	if len(validISvcs) == 0 && len(existentVs.Name) != 0 {
		err = r.Delete(ctx, existentVs, client.PropagationPolicy(metav1.DeletePropagationBackground))
		if err != nil {
			return nil, err
		}

		return nil, nil
	}

	// Validate that canary traffic percentages sum up 100
	if len(validISvcs) > 1 {
		canarySum := int64(0)
		for _, isvc := range validISvcs {
			if percentStr, ok := isvc.Annotations[InferenceServiceSplitPercentAnnotation]; ok {
				percent, parseErr := strconv.ParseInt(percentStr, 10, 32)
				if parseErr != nil {
					return nil, parseErr
				}
				canarySum += percent
			} else {
				return nil, fmt.Errorf("cannot configure traffic splitting for model-tag <%s> because InferenceService <%s> does not have the %s annotation", modelTag, isvc.Name, InferenceServiceSplitPercentAnnotation)
			}
		}

		if canarySum != 100 {
			return nil, fmt.Errorf("cannot configure traffic splitting for model-tag <%s> because split percentages of the group does not sum 100", modelTag)
		}
	}

	// Build the VirtualService for traffic splitting
	desiredVirtualService, err := buildTrafficSplittingVirtualService(modelTag, validISvcs)
	if err != nil {
		return nil, err
	}

	// TODO: Something should control if the route is created or not. What criteria to use?
	var findGatewayErr error
	desiredVirtualService.Spec.Gateways, findGatewayErr = getIstioGatewaysForNamespace(namespace)
	if findGatewayErr != nil {
		return nil, findGatewayErr
	}

	// Create the VirtualService if it does not already exist
	if len(existentVs.Name) == 0 {
		// Create the VirtualService in the Openshift cluster
		err = r.Create(ctx, desiredVirtualService)
		if err != nil && !apierrs.IsAlreadyExists(err) {
			return nil, err
		}

		return desiredVirtualService, nil
	} else {
		desiredVirtualService.Name = existentVs.Name
		if !CompareInferenceServiceVirtualServices(desiredVirtualService, existentVs) {
			err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
				currentVs := &virtualservicev1.VirtualService{}
				// Get the last VirtualService revision
				getLastVSErr := r.Get(ctx, types.NamespacedName{
					Name:      desiredVirtualService.Name,
					Namespace: namespace.Name,
				}, currentVs)
				if getLastVSErr != nil {
					return getLastVSErr
				}

				currentVs.Spec = *desiredVirtualService.Spec.DeepCopy()
				currentVs.ObjectMeta.Labels = desiredVirtualService.ObjectMeta.Labels
				currentVs.ObjectMeta.Annotations = desiredVirtualService.ObjectMeta.Annotations
				return r.Update(ctx, currentVs)
			})
			if err != nil {
				return nil, err
			}
		}

		return desiredVirtualService, nil
	}
}

// Reconcile will manage the creation, update and deletion of the VirtualService returned
// by the newVirtualService function
func (r *OpenshiftInferenceServiceReconciler) reconcileVirtualService(namespace *v1.Namespace, inferenceservice *inferenceservicev1.InferenceService,
	ctx context.Context, newVirtualService func(service *inferenceservicev1.InferenceService) *virtualservicev1.VirtualService) error {
	// Initialize logger format
	log := r.Log.WithValues("inferenceservice", inferenceservice.Name, "namespace", inferenceservice.Namespace)

	desiredServingRuntime := r.findSupportingRuntimeForISvc(ctx, log, inferenceservice)

	// Generate the desired VirtualService and expose externally if enabled
	desiredVirtualService := newVirtualService(inferenceservice)
	if desiredServingRuntime.Annotations["enable-route"] == "true" {
		var findGatewayErr error
		desiredVirtualService.Spec.Gateways, findGatewayErr = getIstioGatewaysForNamespace(namespace)
		if findGatewayErr != nil {
			return findGatewayErr
		}
	}

	// Create the VirtualService if it does not already exist
	foundVirtualService := &virtualservicev1.VirtualService{}
	justCreated := false
	err := r.Get(ctx, types.NamespacedName{
		Name:      desiredVirtualService.Name,
		Namespace: inferenceservice.Namespace,
	}, foundVirtualService)
	if err != nil {
		if apierrs.IsNotFound(err) {
			log.Info("Creating VirtualService")
			// Add .metatada.ownerReferences to the VirtualService to be deleted by the
			// Kubernetes garbage collector if the Predictor is deleted
			err = ctrl.SetControllerReference(inferenceservice, desiredVirtualService, r.Scheme)
			if err != nil {
				log.Error(err, "Unable to add OwnerReference to the VirtualService")
				return err
			}
			// Create the VirtualService in the Openshift cluster
			err = r.Create(ctx, desiredVirtualService)
			if err != nil && !apierrs.IsAlreadyExists(err) {
				log.Error(err, "Unable to create the VirtualService")
				return err
			}
			justCreated = true
		} else {
			log.Error(err, "Unable to fetch the VirtualService")
			return err
		}
	}

	// Reconcile the VirtualService spec if it has been manually modified
	if !justCreated && !CompareInferenceServiceVirtualServices(desiredVirtualService, foundVirtualService) {
		log.Info("Reconciling VirtualService")
		// Retry the update operation when the ingress controller eventually
		// updates the resource version field
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			// Get the last VirtualService revision
			if err := r.Get(ctx, types.NamespacedName{
				Name:      desiredVirtualService.Name,
				Namespace: inferenceservice.Namespace,
			}, foundVirtualService); err != nil {
				return err
			}
			// Reconcile labels and spec field
			foundVirtualService.Spec = *desiredVirtualService.Spec.DeepCopy()
			foundVirtualService.ObjectMeta.Labels = desiredVirtualService.ObjectMeta.Labels
			return r.Update(ctx, foundVirtualService)
		})
		if err != nil {
			log.Error(err, "Unable to reconcile the VirtualService")
			return err
		}
	}

	return nil
}

// ReconcileVirtualService will manage the creation, update and deletion of the
// VirtualService when the Predictor is reconciled
func (r *OpenshiftInferenceServiceReconciler) ReconcileVirtualService(namespace *v1.Namespace,
	inferenceservice *inferenceservicev1.InferenceService, ctx context.Context) error {
	return r.reconcileVirtualService(namespace, inferenceservice, ctx, NewInferenceServiceVirtualService)
}

func (r *OpenshiftInferenceServiceReconciler) ReconcileTrafficSplitting(
	namespace *v1.Namespace, inferenceservice *inferenceservicev1.InferenceService, ctx context.Context) error {
	// Initialize logger format
	log := r.Log.WithValues("inferenceservice", inferenceservice.Name, "namespace", inferenceservice.Namespace)
	log.Info("Reconciling traffic splitting")

	// Fetch associated VirtualService, if there is one
	associatedVs := &virtualservicev1.VirtualService{}
	if vsName, vsOk := inferenceservice.Annotations[VirtualServiceForTrafficSplitAnnotation]; vsOk {
		err := r.Get(ctx, types.NamespacedName{
			Name:      vsName,
			Namespace: inferenceservice.Namespace,
		}, associatedVs)

		if err != nil && !apierrs.IsNotFound(err) {
			log.Error(err, "Error getting associated VirtualService", "virtualService", vsName)
			return err
		}

		log.Info("Associated VirtualService found", "virtualService", vsName)
	}

	var isvcModelTag, vsModelTag string

	if tag, tagOk := inferenceservice.Labels[InferenceServiceModelTagLabel]; tagOk {
		isvcModelTag = tag
	}

	if tag, tagOk := associatedVs.Annotations[InferenceServiceModelTagLabel]; tagOk {
		vsModelTag = tag
	}

	// If there is an associated (old) VirtualService, it must be updated in two cases:
	// - When the ISVC has a blank model-tag
	//    - This means that the ISVC was un-tagged (i.e. removed from the group)
	// - When the ISVC has a tag that is NOT equal to the VS tag
	//    - This means that the ISVC was re-tagged. It should be removed from a group and added to another one.
	//      Here we deal with removing the ISVC from the old group
	if len(associatedVs.Name) != 0 {
		if len(isvcModelTag) == 0 || isvcModelTag != vsModelTag {
			resultingVs, err := r.updateTrafficSplitVirtualService(namespace, vsModelTag, associatedVs, ctx)
			if err != nil {
				log.Error(err, "Unable to update associated old VirtualService for traffic splitting", "model-tag", vsModelTag, "virtualService", associatedVs.Name)
				return err
			}

			if resultingVs == nil {
				log.Info("VirtualService for traffic splitting was deleted", "model-tag", vsModelTag, "virtualService", associatedVs.Name)
			}

			// Unset associatedVs as it no longer is tied to the isvc
			associatedVs = &virtualservicev1.VirtualService{}
		}
	}

	// If the ISVC has model-tag, a VirtualService must be created or updated in these cases:
	// - When the ISVC is being deleted
	//    - This should cause removal of the ISVC from the traffic split
	//	  - If this ISVC was the last one in the group, the VirtualService should be deleted
	// - When the ISVC was created or updated
	//    - If the ISVC got a model-tag, a VirtualService is going to be created if it does not exist.
	//      When a VirtualService for the group already exists, it is updated to add the ISVC to the split.
	//    - There is also the case when the traffic split precentages are adjusted. The existent
	//      VirtualService is updated.
	vsNameToAssociate := ""
	if len(isvcModelTag) != 0 {
		resultingVs, err := r.updateTrafficSplitVirtualService(namespace, isvcModelTag, associatedVs, ctx)
		if err != nil {
			log.Error(err, "Unable to create or update the VirtualService for traffic splitting", "model-tag", isvcModelTag, "virtualService", associatedVs.Name)
			return err
		}

		if resultingVs != nil {
			vsNameToAssociate = resultingVs.Name
		}
	}

	// Update annotation of the InferenceService to store/remove the virtual service name for traffic splitting
	if vsName := inferenceservice.Annotations[VirtualServiceForTrafficSplitAnnotation]; vsNameToAssociate != vsName {
		inferenceservice.ObjectMeta.Annotations[VirtualServiceForTrafficSplitAnnotation] = vsNameToAssociate
		if len(vsNameToAssociate) == 0 {
			delete(inferenceservice.ObjectMeta.Annotations, VirtualServiceForTrafficSplitAnnotation)
		}
		updateIsvcErr := r.Update(ctx, inferenceservice)
		if updateIsvcErr != nil {
			return updateIsvcErr
		}

		log.Info("VirtualService for traffic splitting set in an annotation of the InferenceService", "virtualService", vsNameToAssociate)
	}

	return nil
}
