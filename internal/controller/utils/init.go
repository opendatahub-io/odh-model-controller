package utils

import (
	kedaapi "github.com/kedacore/keda/v2/apis/keda/v1alpha1"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	kuadrantv1 "github.com/kuadrant/kuadrant-operator/api/v1"
	ocpconfigv1 "github.com/openshift/api/config/v1"
	routev1 "github.com/openshift/api/route/v1"
	templatev1 "github.com/openshift/api/template/v1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	istioclientv1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	corev1 "k8s.io/api/core/v1"
	authv1 "k8s.io/api/rbac/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

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
	utilruntime.Must(kuadrantv1.AddToScheme(s))
	utilruntime.Must(nimv1.SchemeBuilder.AddToScheme(s))
	utilruntime.Must(templatev1.AddToScheme(s))
	utilruntime.Must(kedaapi.AddToScheme(s))
	utilruntime.Must(gatewayapiv1.Install(s))
	utilruntime.Must(istioclientv1alpha3.AddToScheme(s))
	utilruntime.Must(ocpconfigv1.AddToScheme(s))

	// +kubebuilder:scaffold:scheme
}
