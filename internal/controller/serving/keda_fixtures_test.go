package serving

import (
	"context"

	kedaapi "github.com/kedacore/keda/v2/apis/keda/v1alpha1"
	. "github.com/onsi/gomega"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/serving/reconcilers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func makeKedaTestISVC(namespace, name string, enableKedaMetrics bool) *kservev1beta1.InferenceService {
	isvc := &kservev1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Annotations: map[string]string{
				"serving.kserve.io/deploymentMode": "RawDeployment",
			},
		},
		Spec: kservev1beta1.InferenceServiceSpec{
			Predictor: kservev1beta1.PredictorSpec{
				// Model field is required for KServe, even if not directly used by KEDA logic
				Model: &kservev1beta1.ModelSpec{
					ModelFormat: kservev1beta1.ModelFormat{Name: "onnx"},
					Runtime:     ptr.To("kserve-ovms"),
					PredictorExtensionSpec: kservev1beta1.PredictorExtensionSpec{
						StorageURI: ptr.To("s3://modelmesh-example-models/onnx/mnist-8"),
					},
				},
			},
		},
	}
	if enableKedaMetrics {
		isvc.Spec.Predictor.MinReplicas = ptr.To[int32](1)
		isvc.Spec.Predictor.MaxReplicas = 5
		isvc.Spec.Predictor.AutoScaling = &kservev1beta1.AutoScalingSpec{
			Metrics: []kservev1beta1.MetricsSpec{
				{
					Type: kservev1beta1.ExternalMetricSourceType,
					External: &kservev1beta1.ExternalMetricSource{
						Metric: kservev1beta1.ExternalMetrics{
							Backend:       kservev1beta1.PrometheusBackend,
							ServerAddress: "https://thanos-querier.openshift-monitoring.svc.cluster.local:9092",
							Query:         `sum(rate(http_requests_total{namespace="kserve-keda-prometheus", service="sample-app-service"}[1m]))`,
						},
						Target: kservev1beta1.MetricTarget{
							Type:  kservev1beta1.AverageValueMetricType,
							Value: ptr.To(resource.MustParse("2")),
						},
					},
				},
			},
		}
	} else {
		// To disable KEDA metrics, we can remove AutoScaling
		// This makes hasPrometheusExternalAutoscalingMetric return false
		isvc.Spec.Predictor.AutoScaling = nil
	}
	return isvc
}

// getAllKedaTestResources fetches all KEDA resources by their known names.
func getAllKedaTestResources(ctx context.Context, k8sClient client.Client, namespace string) map[string]client.Object {
	resources := make(map[string]client.Object)
	sa := &corev1.ServiceAccount{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: reconcilers.KEDAPrometheusAuthServiceAccountName, Namespace: namespace}, sa); err == nil {
		resources["ServiceAccount"] = sa
	}
	secret := &corev1.Secret{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: reconcilers.KEDAPrometheusAuthTriggerSecretName, Namespace: namespace}, secret); err == nil {
		resources["Secret"] = secret
	}
	role := &rbacv1.Role{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: reconcilers.KEDAPrometheusAuthMetricsReaderRoleName, Namespace: namespace}, role); err == nil {
		resources["Role"] = role
	}
	rb := &rbacv1.RoleBinding{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: reconcilers.KEDAPrometheusAuthMetricsReaderRoleBindingName, Namespace: namespace}, rb); err == nil {
		resources["RoleBinding"] = rb
	}
	ta := &kedaapi.TriggerAuthentication{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: reconcilers.KEDAPrometheusAuthTriggerAuthName, Namespace: namespace}, ta); err == nil {
		resources["TriggerAuthentication"] = ta
	}
	return resources
}

func createTestKedaSA(ctx context.Context, k8sClient client.Client, namespace string, isvcOwner *kservev1beta1.InferenceService, otherOwner *metav1.OwnerReference) *corev1.ServiceAccount {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      reconcilers.KEDAPrometheusAuthResourceName,
			Namespace: namespace,
		},
	}
	if isvcOwner != nil {
		sa.OwnerReferences = append(sa.OwnerReferences, reconcilers.AsIsvcOwnerRef(isvcOwner))
	}
	if otherOwner != nil {
		sa.OwnerReferences = append(sa.OwnerReferences, *otherOwner)
	}
	Expect(k8sClient.Create(ctx, sa)).Should(Succeed())
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sa), sa)).Should(Succeed())
	return sa
}

func createTestKedaSecret(ctx context.Context, k8sClient client.Client, namespace string, isvcOwner *kservev1beta1.InferenceService, otherOwner *metav1.OwnerReference) *corev1.Secret {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      reconcilers.KEDAPrometheusAuthTriggerSecretName,
			Namespace: namespace,
			Annotations: map[string]string{
				corev1.ServiceAccountNameKey: reconcilers.KEDAPrometheusAuthResourceName,
			},
		},
		Type: corev1.SecretTypeServiceAccountToken,
	}
	if isvcOwner != nil {
		secret.OwnerReferences = append(secret.OwnerReferences, reconcilers.AsIsvcOwnerRef(isvcOwner))
	}
	if otherOwner != nil {
		secret.OwnerReferences = append(secret.OwnerReferences, *otherOwner)
	}
	Expect(k8sClient.Create(ctx, secret)).Should(Succeed())
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(secret), secret)).Should(Succeed())
	return secret
}

func createTestKedaRole(ctx context.Context, k8sClient client.Client, namespace string, isvcOwner *kservev1beta1.InferenceService, otherOwner *metav1.OwnerReference) *rbacv1.Role {
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      reconcilers.KEDAPrometheusAuthMetricsReaderRoleName,
			Namespace: namespace,
		},
		Rules: []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}}}, // Simplified rule
	}
	if isvcOwner != nil {
		role.OwnerReferences = append(role.OwnerReferences, reconcilers.AsIsvcOwnerRef(isvcOwner))
	}
	if otherOwner != nil {
		role.OwnerReferences = append(role.OwnerReferences, *otherOwner)
	}
	Expect(k8sClient.Create(ctx, role)).Should(Succeed())
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(role), role)).Should(Succeed())
	return role
}

func createTestKedaRoleBinding(ctx context.Context, k8sClient client.Client, namespace string, isvcOwner *kservev1beta1.InferenceService, otherOwner *metav1.OwnerReference) *rbacv1.RoleBinding {
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      reconcilers.KEDAPrometheusAuthMetricsReaderRoleBindingName,
			Namespace: namespace,
		},
		Subjects: []rbacv1.Subject{{Kind: rbacv1.ServiceAccountKind, Name: reconcilers.KEDAPrometheusAuthResourceName, Namespace: namespace}},
		RoleRef:  rbacv1.RoleRef{Kind: "Role", Name: reconcilers.KEDAPrometheusAuthMetricsReaderRoleName, APIGroup: rbacv1.GroupName},
	}
	if isvcOwner != nil {
		rb.OwnerReferences = append(rb.OwnerReferences, reconcilers.AsIsvcOwnerRef(isvcOwner))
	}
	if otherOwner != nil {
		rb.OwnerReferences = append(rb.OwnerReferences, *otherOwner)
	}
	Expect(k8sClient.Create(ctx, rb)).Should(Succeed())
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(rb), rb)).Should(Succeed())
	return rb
}

func createTestKedaTA(ctx context.Context, k8sClient client.Client, namespace string, isvcOwner *kservev1beta1.InferenceService, otherOwner *metav1.OwnerReference) *kedaapi.TriggerAuthentication {
	ta := &kedaapi.TriggerAuthentication{
		ObjectMeta: metav1.ObjectMeta{
			Name:      reconcilers.KEDAPrometheusAuthTriggerAuthName,
			Namespace: namespace,
		},
		Spec: kedaapi.TriggerAuthenticationSpec{
			SecretTargetRef: []kedaapi.AuthSecretTargetRef{
				{Parameter: "bearerToken", Name: reconcilers.KEDAPrometheusAuthTriggerSecretName, Key: "token"},
			},
		},
	}
	if isvcOwner != nil {
		ta.OwnerReferences = append(ta.OwnerReferences, reconcilers.AsIsvcOwnerRef(isvcOwner))
	}
	if otherOwner != nil {
		ta.OwnerReferences = append(ta.OwnerReferences, *otherOwner)
	}
	Expect(k8sClient.Create(ctx, ta)).Should(Succeed())
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(ta), ta)).Should(Succeed())
	return ta
}
