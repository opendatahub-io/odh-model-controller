package utils

import (
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	authorinov1beta2 "github.com/kuadrant/authorino/api/v1beta2"
	routev1 "github.com/openshift/api/route/v1"
	templatev1 "github.com/openshift/api/template/v1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	istioclientv1beta1 "istio.io/client-go/pkg/apis/networking/v1beta1"
	istiosecurityv1beta1 "istio.io/client-go/pkg/apis/security/v1beta1"
	telemetryv1alpha1 "istio.io/client-go/pkg/apis/telemetry/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	authv1 "k8s.io/api/rbac/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	knservingv1 "knative.dev/serving/pkg/apis/serving/v1"
	maistrav1 "maistra.io/api/core/v1"

	nimv1 "github.com/opendatahub-io/odh-model-controller/api/nim/v1"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
)

// RegisterSchemes adds schemes of used resources to controller's scheme.
func RegisterSchemes(s *runtime.Scheme) {

	utilruntime.Must(clientgoscheme.AddToScheme(s))

	utilruntime.Must(kservev1alpha1.AddToScheme(s))
	utilruntime.Must(kservev1beta1.AddToScheme(s))
	utilruntime.Must(corev1.AddToScheme(s))
	utilruntime.Must(routev1.AddToScheme(s))
	utilruntime.Must(authv1.AddToScheme(s))
	utilruntime.Must(monitoringv1.AddToScheme(s))
	utilruntime.Must(istiosecurityv1beta1.AddToScheme(s))
	utilruntime.Must(telemetryv1alpha1.AddToScheme(s))
	utilruntime.Must(maistrav1.SchemeBuilder.AddToScheme(s))
	utilruntime.Must(knservingv1.AddToScheme(s))
	utilruntime.Must(authorinov1beta2.SchemeBuilder.AddToScheme(s))
	utilruntime.Must(istioclientv1beta1.SchemeBuilder.AddToScheme(s))
	utilruntime.Must(nimv1.SchemeBuilder.AddToScheme(s))
	utilruntime.Must(templatev1.AddToScheme(s))

	// The following are related to Service Mesh, uncomment this and other
	// similar blocks to use with Service Mesh
	// utilruntime.Must(virtualservicev1.AddToScheme(scheme))

	// +kubebuilder:scaffold:scheme
}
