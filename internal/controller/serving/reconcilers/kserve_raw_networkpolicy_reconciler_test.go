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
	"errors"
	"strconv"
	"strings"

	"github.com/go-logr/logr"
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	kserveconstants "github.com/kserve/kserve/pkg/constants"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	routev1 "github.com/openshift/api/route/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	controllerutils "github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

type recordingSubReconciler struct {
	NoResourceRemoval
	reconcile func() error
}

func (r *recordingSubReconciler) Reconcile(_ context.Context, _ logr.Logger, _ *kservev1beta1.InferenceService) error {
	return r.reconcile()
}

var _ = Describe("KserveRawNetworkPolicyReconciler", func() {
	const (
		routeDeleteFailure  = "delete failure"
		policyUpdateFailure = "update"
	)
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		controllerutils.RegisterSchemes(scheme)
		Expect(routev1.AddToScheme(scheme)).To(Succeed())
	})

	newISVC := func() *kservev1beta1.InferenceService {
		return &kservev1beta1.InferenceService{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "raw-model",
				Namespace: "models",
				UID:       types.UID("raw-model-uid"),
				Labels: map[string]string{
					constants.KserveNetworkVisibility: constants.LabelEnableKserveRawRoute,
				},
			},
		}
	}

	newService := func(name, component string, ports ...corev1.ServicePort) *corev1.Service {
		return &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "models",
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: kservev1beta1.SchemeGroupVersion.String(),
					Kind:       constants.InferenceServiceKind,
					Name:       "raw-model",
					UID:        types.UID("raw-model-uid"),
					Controller: ptr.To(true),
				}},
				Labels: map[string]string{
					constants.KserveGroupAnnotation:        "raw-model",
					kserveconstants.KServiceComponentLabel: component,
				},
			},
			Spec: corev1.ServiceSpec{Ports: ports},
		}
	}

	newServingRuntime := func() *kservev1alpha1.ServingRuntime {
		return &kservev1alpha1.ServingRuntime{
			ObjectMeta: metav1.ObjectMeta{Name: "raw-runtime", Namespace: "models"},
			Spec: kservev1alpha1.ServingRuntimeSpec{
				ServingRuntimePodSpec: kservev1alpha1.ServingRuntimePodSpec{
					Annotations: map[string]string{
						kserveconstants.PrometheusPortAnnotationKey: "8000",
					},
				},
			},
		}
	}

	DescribeTable("preserves only the ClusterLocal front-door port for cluster clients", func(ctx SpecContext, transformer, auth bool) {
		isvc := newISVC()
		isvc.Labels[kserveconstants.NetworkVisibility] = kserveconstants.ClusterLocalVisibility
		isvc.Annotations = map[string]string{constants.EnableAuthODHAnnotation: strconv.FormatBool(auth)}
		predictor := newService("raw-model-predictor", predictorNetworkPolicyComponent,
			corev1.ServicePort{Name: "http", Port: 80, TargetPort: intstr.FromInt32(8080)},
			corev1.ServicePort{Name: "https", Port: 443, TargetPort: intstr.FromInt32(8443)},
		)
		objects := []client.Object{isvc, predictor}
		component := predictorNetworkPolicyComponent
		expectedPort := "TCP:8080"
		if auth {
			expectedPort = "TCP:8443"
		}
		if transformer {
			isvc.Spec.Transformer = &kservev1beta1.TransformerSpec{}
			objects = append(objects, newService("raw-model-transformer", transformerNetworkPolicyComponent,
				corev1.ServicePort{Name: "http", Port: 80, TargetPort: intstr.FromInt32(9090)},
				corev1.ServicePort{Name: "https", Port: 443, TargetPort: intstr.FromInt32(9443)},
			))
			component = transformerNetworkPolicyComponent
			expectedPort = "TCP:9090"
			if auth {
				expectedPort = "TCP:9443"
			}
		}
		cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
		reconciler := NewKserveRawNetworkPolicyReconciler(cli)
		Expect(reconciler.Reconcile(ctx, log.Log, isvc)).To(Succeed())
		policy := &networkingv1.NetworkPolicy{}
		key := client.ObjectKey{Name: rawNetworkPolicyName(isvc, component), Namespace: isvc.Namespace}
		Expect(cli.Get(ctx, key, policy)).To(Succeed())
		Expect(policy.Spec.Ingress).To(HaveLen(1))
		Expect(policy.Spec.Ingress[0].From).To(Equal([]networkingv1.NetworkPolicyPeer{{
			NamespaceSelector: &metav1.LabelSelector{}, PodSelector: &metav1.LabelSelector{},
		}}))
		Expect(policyPortStrings(policy.Spec.Ingress[0].Ports)).To(Equal([]string{expectedPort}))
		if transformer {
			backend := &networkingv1.NetworkPolicy{}
			Expect(cli.Get(ctx, client.ObjectKey{Name: rawNetworkPolicyName(isvc, predictorNetworkPolicyComponent), Namespace: isvc.Namespace}, backend)).To(Succeed())
			Expect(backend.Spec.Ingress).To(HaveLen(1))
			Expect(backend.Spec.Ingress[0].From[0].NamespaceSelector).To(BeNil())
			Expect(backend.Spec.Ingress[0].From[0].PodSelector.MatchLabels).To(Equal(map[string]string{
				constants.KserveGroupAnnotation: isvc.Name, kserveconstants.KServiceComponentLabel: transformerNetworkPolicyComponent,
			}))
		}

		isvc.Labels[kserveconstants.NetworkVisibility] = constants.LabelEnableKserveRawRoute
		Expect(reconciler.Reconcile(ctx, log.Log, isvc)).To(Succeed())
		Expect(cli.Get(ctx, key, policy)).To(Succeed())
		Expect(policy.Spec.Ingress).To(HaveLen(1))
		Expect(policy.Spec.Ingress[0].From).To(Equal([]networkingv1.NetworkPolicyPeer{routerPeer()}))
	},
		Entry("plain predictor", false, false),
		Entry("authenticated predictor", false, true),
		Entry("plain transformer", true, false),
		Entry("authenticated transformer", true, true),
	)

	DescribeTable("permits only the configured ISVC explainer to call its predictor", func(ctx SpecContext, transformer, auth bool) {
		isvc := newISVC()
		delete(isvc.Labels, constants.KserveNetworkVisibility)
		isvc.Spec.Explainer = &kservev1beta1.ExplainerSpec{}
		isvc.Annotations = map[string]string{constants.EnableAuthODHAnnotation: strconv.FormatBool(auth)}
		predictor := newService("raw-model-predictor", predictorNetworkPolicyComponent,
			corev1.ServicePort{Name: "http", Port: 80, TargetPort: intstr.FromInt32(8080)},
			corev1.ServicePort{Name: "https", Port: 443, TargetPort: intstr.FromInt32(8443)},
		)
		objects := []client.Object{isvc, predictor}
		expectedRules := 1
		if transformer {
			isvc.Spec.Transformer = &kservev1beta1.TransformerSpec{}
			objects = append(objects, newService("raw-model-transformer", transformerNetworkPolicyComponent,
				corev1.ServicePort{Name: "http", Port: 80, TargetPort: intstr.FromInt32(9090)},
			))
			expectedRules++
		}
		cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
		reconciler := NewKserveRawNetworkPolicyReconciler(cli)
		Expect(reconciler.Reconcile(ctx, log.Log, isvc)).To(Succeed())
		policy := &networkingv1.NetworkPolicy{}
		key := client.ObjectKey{Name: rawNetworkPolicyName(isvc, predictorNetworkPolicyComponent), Namespace: isvc.Namespace}
		Expect(cli.Get(ctx, key, policy)).To(Succeed())
		Expect(policy.Spec.Ingress).To(HaveLen(expectedRules))
		rule := policy.Spec.Ingress[expectedRules-1]
		Expect(rule.From).To(HaveLen(1))
		Expect(rule.From[0].NamespaceSelector).To(BeNil())
		Expect(rule.From[0].PodSelector.MatchLabels).To(Equal(map[string]string{
			constants.KserveGroupAnnotation: isvc.Name, kserveconstants.KServiceComponentLabel: string(kservev1beta1.ExplainerComponent),
		}))
		expectedPort := "TCP:8080"
		if auth {
			expectedPort = "TCP:8443"
		}
		Expect(policyPortStrings(rule.Ports)).To(Equal([]string{expectedPort}))
		selector, err := metav1.LabelSelectorAsSelector(rule.From[0].PodSelector)
		Expect(err).NotTo(HaveOccurred())
		Expect(selector.Matches(labels.Set{
			constants.KserveGroupAnnotation: "other-model", kserveconstants.KServiceComponentLabel: string(kservev1beta1.ExplainerComponent),
		})).To(BeFalse())
		Expect(policy.Spec.PodSelector.MatchLabels[kserveconstants.KServiceComponentLabel]).To(Equal(predictorNetworkPolicyComponent))

		isvc.Spec.Explainer = nil
		Expect(reconciler.Reconcile(ctx, log.Log, isvc)).To(Succeed())
		Expect(cli.Get(ctx, key, policy)).To(Succeed())
		Expect(policy.Spec.Ingress).To(HaveLen(expectedRules - 1))
	},
		Entry("plain predictor", false, false),
		Entry("authenticated predictor", false, true),
		Entry("plain predictor behind a transformer", true, false),
		Entry("authenticated predictor behind a transformer", true, true),
	)

	It("creates per-component policies with target ports, canary union, Route, and metrics peers", func(ctx SpecContext) {
		isvc := newISVC()
		isvc.Annotations = map[string]string{constants.EnableAuthODHAnnotation: "true"}
		runtimeName := "raw-runtime"
		isvc.Spec.Predictor.Model = &kservev1beta1.ModelSpec{
			Runtime:     ptr.To(runtimeName),
			ModelFormat: kservev1beta1.ModelFormat{Name: "test"},
		}
		isvc.Spec.Transformer = &kservev1beta1.TransformerSpec{}
		isvc.Spec.Canary = []kservev1beta1.CanarySpec{
			{Predictor: kservev1beta1.PredictorSpec{Name: "v2"}, TrafficPercent: 10},
		}

		predictor := newService("raw-model-predictor", "predictor", corev1.ServicePort{
			Name: "https", Port: 8443, TargetPort: intstr.FromString("serving"),
		})
		canary := newService("raw-model-v2-predictor", "predictor", corev1.ServicePort{
			Name: "https", Port: 8444, TargetPort: intstr.FromString("serving-canary"),
		})
		transformer := newService("raw-model-transformer", "transformer", corev1.ServicePort{
			Name: "https", Port: 8443, TargetPort: intstr.FromString("transformer"),
		})
		explainer := newService("raw-model-explainer", "explainer", corev1.ServicePort{
			Name: "https", Port: 8443, TargetPort: intstr.FromString("explainer"),
		})
		metrics := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "raw-model-metrics",
				Namespace: "models",
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: kservev1beta1.SchemeGroupVersion.String(),
					Kind:       constants.InferenceServiceKind,
					Name:       "raw-model",
					UID:        types.UID("raw-model-uid"),
					Controller: ptr.To(true),
				}},
			},
			Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{
				Name: "metrics", Port: 80, TargetPort: intstr.FromInt32(9100),
			}}},
		}

		cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			isvc, newServingRuntime(), predictor, canary, transformer, explainer, metrics,
		).Build()
		reconciler := NewKserveRawNetworkPolicyReconciler(cli)
		Expect(reconciler.Reconcile(ctx, log.Log, isvc)).To(Succeed())

		predictorPolicy := &networkingv1.NetworkPolicy{}
		Expect(cli.Get(ctx, client.ObjectKey{Name: rawNetworkPolicyName(isvc, predictorNetworkPolicyComponent), Namespace: isvc.Namespace}, predictorPolicy)).To(Succeed())
		transformerPolicy := &networkingv1.NetworkPolicy{}
		Expect(cli.Get(ctx, client.ObjectKey{Name: rawNetworkPolicyName(isvc, transformerNetworkPolicyComponent), Namespace: isvc.Namespace}, transformerPolicy)).To(Succeed())

		Expect(predictorPolicy.Spec.PodSelector.MatchLabels).To(Equal(map[string]string{
			constants.KserveGroupAnnotation:        isvc.Name,
			kserveconstants.KServiceComponentLabel: predictorNetworkPolicyComponent,
		}))
		Expect(predictorPolicy.Spec.PolicyTypes).To(ConsistOf(networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress))
		Expect(predictorPolicy.Spec.Egress).To(HaveLen(1))
		Expect(predictorPolicy.Spec.Egress[0].To).To(BeEmpty())
		Expect(predictorPolicy.Spec.Ingress).To(HaveLen(3))
		Expect(predictorPolicy.Spec.Ingress[0].From[0].NamespaceSelector.MatchLabels).To(Equal(map[string]string{
			openshiftIngressPolicyGroupLabel: "",
		}))
		Expect(policyPortStrings(predictorPolicy.Spec.Ingress[0].Ports)).To(ConsistOf("TCP:serving", "TCP:serving-canary"))
		Expect(predictorPolicy.Spec.Ingress[1].From[0].PodSelector.MatchLabels).To(Equal(map[string]string{
			constants.KserveGroupAnnotation:        isvc.Name,
			kserveconstants.KServiceComponentLabel: transformerNetworkPolicyComponent,
		}))
		Expect(policyPortStrings(predictorPolicy.Spec.Ingress[1].Ports)).To(ConsistOf("TCP:serving", "TCP:serving-canary"))
		Expect(policyPortStrings(predictorPolicy.Spec.Ingress[2].Ports)).To(ConsistOf("TCP:9100"))
		Expect(predictorPolicy.Spec.Ingress[1].From[0].NamespaceSelector).To(BeNil())

		Expect(transformerPolicy.Spec.PodSelector.MatchLabels[kserveconstants.KServiceComponentLabel]).To(Equal(transformerNetworkPolicyComponent))
		Expect(transformerPolicy.Spec.Ingress).To(HaveLen(2))
		Expect(transformerPolicy.Spec.Ingress[0].From[0].NamespaceSelector.MatchLabels).To(Equal(map[string]string{
			openshiftIngressPolicyGroupLabel: "",
		}))
		Expect(policyPortStrings(transformerPolicy.Spec.Ingress[0].Ports)).To(ConsistOf("TCP:transformer"))
		Expect(policyPortStrings(transformerPolicy.Spec.Ingress[1].Ports)).To(ConsistOf("TCP:9100"))
		Expect(transformerPolicy.OwnerReferences).To(HaveLen(1))
		Expect(transformerPolicy.OwnerReferences[0].UID).To(Equal(isvc.UID))
		Expect(*transformerPolicy.OwnerReferences[0].Controller).To(BeTrue())
	})

	It("uses the unauthenticated Service target port and excludes an auth proxy port", func(ctx SpecContext) {
		isvc := newISVC()
		predictor := newService("raw-model-predictor", "predictor",
			corev1.ServicePort{Name: "http", Port: 80, TargetPort: intstr.FromInt32(8080)},
			corev1.ServicePort{Name: "https", Port: 8443, TargetPort: intstr.FromInt32(8443)},
		)
		cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(isvc, predictor).Build()
		Expect(NewKserveRawNetworkPolicyReconciler(cli).Reconcile(ctx, log.Log, isvc)).To(Succeed())

		policy := &networkingv1.NetworkPolicy{}
		Expect(cli.Get(ctx, client.ObjectKey{Name: rawNetworkPolicyName(isvc, predictorNetworkPolicyComponent), Namespace: isvc.Namespace}, policy)).To(Succeed())
		Expect(policy.Spec.Ingress).To(HaveLen(1))
		Expect(policyPortStrings(policy.Spec.Ingress[0].Ports)).To(ConsistOf("TCP:8080"))
	})

	It("keeps the baseline policy when the Prometheus port annotation becomes invalid", func(ctx SpecContext) {
		isvc := newISVC()
		isvc.Spec.Predictor.Model = &kservev1beta1.ModelSpec{
			Runtime:     ptr.To("raw-runtime"),
			ModelFormat: kservev1beta1.ModelFormat{Name: "test"},
		}
		runtime := newServingRuntime()
		predictor := newService("raw-model-predictor", "predictor", corev1.ServicePort{
			Name: "http", Port: 80, TargetPort: intstr.FromInt32(8080),
		})
		unownedMetrics := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "raw-model-metrics", Namespace: isvc.Namespace},
			Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{
				Name: "metrics", Port: 80, TargetPort: intstr.FromInt32(9999),
			}}},
		}
		cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(isvc, runtime, predictor, unownedMetrics).Build()
		reconciler := NewKserveRawNetworkPolicyReconciler(cli)
		Expect(reconciler.Reconcile(ctx, log.Log, isvc)).To(Succeed())

		policy := &networkingv1.NetworkPolicy{}
		policyKey := client.ObjectKey{Name: rawNetworkPolicyName(isvc, predictorNetworkPolicyComponent), Namespace: isvc.Namespace}
		Expect(cli.Get(ctx, policyKey, policy)).To(Succeed())
		Expect(policy.Spec.Ingress).To(HaveLen(2))
		Expect(policyPortStrings(policy.Spec.Ingress[1].Ports)).To(ConsistOf("TCP:8000"))

		runtime.Spec.ServingRuntimePodSpec.Annotations[kserveconstants.PrometheusPortAnnotationKey] = "not-a-port"
		Expect(cli.Update(ctx, runtime)).To(Succeed())
		Expect(reconciler.Reconcile(ctx, log.Log, isvc)).To(Succeed())

		Expect(cli.Get(ctx, policyKey, policy)).To(Succeed())
		Expect(policy.Spec.Ingress).To(HaveLen(1))
		Expect(policyPortStrings(policy.Spec.Ingress[0].Ports)).To(ConsistOf("TCP:8080"))
		Expect(policy.Spec.Egress).To(HaveLen(1))
	})

	It("creates deny-ingress policy before a backing Service exists", func(ctx SpecContext) {
		isvc := newISVC()
		cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(isvc).Build()
		Expect(NewKserveRawNetworkPolicyReconciler(cli).Reconcile(ctx, log.Log, isvc)).To(Succeed())

		policy := &networkingv1.NetworkPolicy{}
		Expect(cli.Get(ctx, client.ObjectKey{Name: rawNetworkPolicyName(isvc, predictorNetworkPolicyComponent), Namespace: isvc.Namespace}, policy)).To(Succeed())
		Expect(policy.Spec.Ingress).To(BeEmpty())
		Expect(policy.Spec.Egress).To(HaveLen(1))
	})

	It("selects only the requested ISVC component Pods", func(ctx SpecContext) {
		isvc := newISVC()
		cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(isvc).Build()
		Expect(NewKserveRawNetworkPolicyReconciler(cli).Reconcile(ctx, log.Log, isvc)).To(Succeed())

		policy := &networkingv1.NetworkPolicy{}
		Expect(cli.Get(ctx, client.ObjectKey{Name: rawNetworkPolicyName(isvc, predictorNetworkPolicyComponent), Namespace: isvc.Namespace}, policy)).To(Succeed())
		selector, err := metav1.LabelSelectorAsSelector(&policy.Spec.PodSelector)
		Expect(err).NotTo(HaveOccurred())
		Expect(selector.Matches(labels.Set{
			constants.KserveGroupAnnotation:        isvc.Name,
			kserveconstants.KServiceComponentLabel: predictorNetworkPolicyComponent,
		})).To(BeTrue())
		Expect(selector.Matches(labels.Set{
			constants.KserveGroupAnnotation:        isvc.Name,
			kserveconstants.KServiceComponentLabel: transformerNetworkPolicyComponent,
		})).To(BeFalse())
		Expect(selector.Matches(labels.Set{
			constants.KserveGroupAnnotation:        isvc.Name,
			kserveconstants.KServiceComponentLabel: "explainer",
		})).To(BeFalse())
		Expect(selector.Matches(labels.Set{
			constants.KserveGroupAnnotation:        "another-isvc",
			kserveconstants.KServiceComponentLabel: predictorNetworkPolicyComponent,
		})).To(BeFalse())
	})

	It("ignores a same-label Service that is not owned by this ISVC", func(ctx SpecContext) {
		isvc := newISVC()
		owned := newService("raw-model-predictor", predictorNetworkPolicyComponent, corev1.ServicePort{
			Name: "http", Port: 80, TargetPort: intstr.FromInt32(8080),
		})
		unowned := newService("raw-model-user-predictor", predictorNetworkPolicyComponent, corev1.ServicePort{
			Name: "http", Port: 80, TargetPort: intstr.FromInt32(9999),
		})
		unowned.OwnerReferences = nil
		cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(isvc, owned, unowned).Build()
		Expect(NewKserveRawNetworkPolicyReconciler(cli).Reconcile(ctx, log.Log, isvc)).To(Succeed())

		policy := &networkingv1.NetworkPolicy{}
		Expect(cli.Get(ctx, client.ObjectKey{Name: rawNetworkPolicyName(isvc, predictorNetworkPolicyComponent), Namespace: isvc.Namespace}, policy)).To(Succeed())
		Expect(policyPortStrings(policy.Spec.Ingress[0].Ports)).To(ConsistOf("TCP:8080"))
	})

	It("allows all TCP, UDP, and SCTP ports between predictor Pods of a multi-node ISVC", func(ctx SpecContext) {
		isvc := newISVC()
		isvc.Spec.Predictor.WorkerSpec = &kservev1beta1.WorkerSpec{}
		predictor := newService("raw-model-predictor", predictorNetworkPolicyComponent, corev1.ServicePort{
			Name: "http", Port: 80, TargetPort: intstr.FromInt32(8080),
		})
		head := newService("raw-model-head", predictorNetworkPolicyComponent)
		head.Labels[kserveconstants.MultiNodeRoleLabelKey] = kserveconstants.MultiNodeHead
		worker := newService("raw-model-worker", predictorNetworkPolicyComponent)
		worker.Labels[kserveconstants.MultiNodeRoleLabelKey] = kserveconstants.MultiNodeWorker
		cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(isvc, predictor, head, worker).Build()
		Expect(NewKserveRawNetworkPolicyReconciler(cli).Reconcile(ctx, log.Log, isvc)).To(Succeed())

		policy := &networkingv1.NetworkPolicy{}
		err := cli.Get(ctx, client.ObjectKey{Name: rawNetworkPolicyName(isvc, predictorNetworkPolicyComponent), Namespace: isvc.Namespace}, policy)
		Expect(err).NotTo(HaveOccurred())
		Expect(policy.Spec.Ingress).To(HaveLen(2))
		allPortsRule := policy.Spec.Ingress[1]
		Expect(allPortsRule.From).To(HaveLen(1))
		Expect(allPortsRule.From[0].PodSelector.MatchLabels).To(Equal(map[string]string{
			constants.KserveGroupAnnotation:        isvc.Name,
			kserveconstants.KServiceComponentLabel: predictorNetworkPolicyComponent,
		}))
		Expect(policyPortStrings(allPortsRule.Ports)).To(ConsistOf("TCP:1-65535", "UDP:1-65535", "SCTP:1-65535"))
	})

	It("does not add a multi-node peer for a canary WorkerSpec KServe does not materialize", func(ctx SpecContext) {
		isvc := newISVC()
		isvc.Spec.Canary = []kservev1beta1.CanarySpec{{
			Predictor:      kservev1beta1.PredictorSpec{WorkerSpec: &kservev1beta1.WorkerSpec{}},
			TrafficPercent: 10,
		}}
		predictor := newService("raw-model-predictor", predictorNetworkPolicyComponent, corev1.ServicePort{
			Name: "http", Port: 80, TargetPort: intstr.FromInt32(8080),
		})
		cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(isvc, predictor).Build()
		Expect(NewKserveRawNetworkPolicyReconciler(cli).Reconcile(ctx, log.Log, isvc)).To(Succeed())

		policy := &networkingv1.NetworkPolicy{}
		Expect(cli.Get(ctx, client.ObjectKey{Name: rawNetworkPolicyName(isvc, predictorNetworkPolicyComponent), Namespace: isvc.Namespace}, policy)).To(Succeed())
		Expect(policy.Spec.Ingress).To(HaveLen(1))
		Expect(policyPortStrings(policy.Spec.Ingress[0].Ports)).To(ConsistOf("TCP:8080"))
	})

	It("keeps component policy names valid and distinct for long ISVC names", func() {
		isvc := newISVC()
		isvc.Name = strings.Repeat("model", 20)
		predictorName := rawNetworkPolicyName(isvc, predictorNetworkPolicyComponent)
		transformerName := rawNetworkPolicyName(isvc, transformerNetworkPolicyComponent)
		Expect(len(predictorName)).To(BeNumerically("<=", 63))
		Expect(len(transformerName)).To(BeNumerically("<=", 63))
		Expect(transformerName).NotTo(Equal(predictorName))
	})

	It("repairs owned drift and refuses an unowned same-name policy", func(ctx SpecContext) {
		isvc := newISVC()
		predictor := newService("raw-model-predictor", "predictor", corev1.ServicePort{Name: "http", Port: 80, TargetPort: intstr.FromInt32(8080)})
		owned := &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:            rawNetworkPolicyName(isvc, predictorNetworkPolicyComponent),
				Namespace:       isvc.Namespace,
				OwnerReferences: []metav1.OwnerReference{{APIVersion: kservev1beta1.SchemeGroupVersion.String(), Kind: constants.InferenceServiceKind, Name: isvc.Name, UID: isvc.UID, Controller: ptr.To(true)}},
			},
			Spec: networkingv1.NetworkPolicySpec{PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"wrong": "selector"}}},
		}
		cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(isvc, predictor, owned).Build()
		reconciler := NewKserveRawNetworkPolicyReconciler(cli)
		Expect(reconciler.Reconcile(ctx, log.Log, isvc)).To(Succeed())
		Expect(cli.Get(ctx, client.ObjectKeyFromObject(owned), owned)).To(Succeed())
		Expect(owned.Spec.PodSelector.MatchLabels).To(Equal(map[string]string{
			constants.KserveGroupAnnotation:        isvc.Name,
			kserveconstants.KServiceComponentLabel: predictorNetworkPolicyComponent,
		}))
		Expect(reconciler.Delete(ctx, log.Log, isvc)).To(Succeed())
		deleted := &networkingv1.NetworkPolicy{}
		err := cli.Get(ctx, client.ObjectKeyFromObject(owned), deleted)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
		Expect(reconciler.Reconcile(ctx, log.Log, isvc)).To(Succeed())
		Expect(cli.Get(ctx, client.ObjectKeyFromObject(owned), deleted)).To(Succeed())

		isvc.Spec.Transformer = &kservev1beta1.TransformerSpec{}
		unowned := &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      rawNetworkPolicyName(isvc, transformerNetworkPolicyComponent),
				Namespace: isvc.Namespace,
			},
			Spec: networkingv1.NetworkPolicySpec{PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"owner": "somebody-else"}}},
		}
		Expect(cli.Create(ctx, unowned)).To(Succeed())
		Expect(NewKserveRawNetworkPolicyReconciler(cli).Reconcile(ctx, log.Log, isvc)).To(MatchError(ContainSubstring("refusing to overwrite")))
		Expect(cli.Get(ctx, client.ObjectKeyFromObject(unowned), unowned)).To(Succeed())
		Expect(unowned.Spec.PodSelector.MatchLabels).To(Equal(map[string]string{"owner": "somebody-else"}))
	})

	It("stops before Route reconciliation when policy ownership collides", func(ctx SpecContext) {
		isvc := newISVC()
		predictor := newService("raw-model-predictor", "predictor", corev1.ServicePort{Name: "http", Port: 80, TargetPort: intstr.FromInt32(8080)})
		collision := &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      rawNetworkPolicyName(isvc, predictorNetworkPolicyComponent),
				Namespace: isvc.Namespace,
			},
		}
		cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(isvc, predictor, collision).Build()
		Expect(NewKServeRawInferenceServiceReconciler(cli).Reconcile(ctx, log.Log, isvc)).To(HaveOccurred())
		route := &routev1.Route{}
		err := cli.Get(ctx, client.ObjectKey{Name: isvc.Name, Namespace: isvc.Namespace}, route)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	DescribeTable("honors disabled exposure despite policy failure", func(ctx SpecContext, scenario string) {
		isvc := newISVC()
		route := &routev1.Route{
			ObjectMeta: metav1.ObjectMeta{
				Name:      isvc.Name,
				Namespace: isvc.Namespace,
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: kservev1beta1.SchemeGroupVersion.String(),
					Kind:       constants.InferenceServiceKind,
					Name:       isvc.Name,
					UID:        isvc.UID,
					Controller: ptr.To(true),
				}},
			},
		}
		delete(isvc.Labels, constants.KserveNetworkVisibility)
		if scenario == "unowned" {
			route.OwnerReferences = nil
		}
		base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(isvc).Build()
		if scenario != "missing" {
			Expect(base.Create(ctx, route)).To(Succeed())
		}
		policyErr := errors.New("graph listing failed")
		cleanupErr := errors.New("route deletion failed")
		cli := interceptor.NewClient(base, interceptor.Funcs{
			List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if _, ok := list.(*kservev1alpha1.InferenceGraphList); ok {
					return policyErr
				}
				return c.List(ctx, list, opts...)
			},
			Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				if _, ok := obj.(*routev1.Route); ok && scenario == routeDeleteFailure {
					return cleanupErr
				}
				return c.Delete(ctx, obj, opts...)
			},
		})
		reconciler := &KserveRawInferenceServiceReconciler{subResourceReconcilers: []SubResourceReconciler{
			NewKserveRawNetworkPolicyReconciler(cli), NewKserveRawRouteReconciler(cli),
		}}
		err := reconciler.Reconcile(ctx, log.Log, isvc)
		Expect(errors.Is(err, policyErr)).To(BeTrue())
		Expect(errors.Is(err, cleanupErr)).To(Equal(scenario == routeDeleteFailure))
		actual := &routev1.Route{}
		err = base.Get(ctx, client.ObjectKeyFromObject(route), actual)
		if scenario == "unowned" || scenario == routeDeleteFailure {
			Expect(err).NotTo(HaveOccurred())
			Expect(actual.Spec).To(Equal(route.Spec))
			Expect(actual.OwnerReferences).To(Equal(route.OwnerReferences))
		} else {
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		}
	},
		Entry("deletes an owned Route", "owned"),
		Entry("tolerates a missing Route", "missing"),
		Entry("preserves an unowned Route", "unowned"),
		Entry("aggregates policy and cleanup errors", routeDeleteFailure),
	)

	It("preserves an owned Route when policy ownership collides", func(ctx SpecContext) {
		isvc := newISVC()
		ownedRoute := &routev1.Route{
			ObjectMeta: metav1.ObjectMeta{
				Name:      isvc.Name,
				Namespace: isvc.Namespace,
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: kservev1beta1.SchemeGroupVersion.String(),
					Kind:       constants.InferenceServiceKind,
					Name:       isvc.Name,
					UID:        isvc.UID,
					Controller: ptr.To(true),
				}},
			},
		}
		collision := &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      rawNetworkPolicyName(isvc, predictorNetworkPolicyComponent),
				Namespace: isvc.Namespace,
			},
		}
		cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(isvc, ownedRoute, collision).Build()
		reconciler := NewKServeRawInferenceServiceReconciler(cli)

		Expect(reconciler.Reconcile(ctx, log.Log, isvc)).To(HaveOccurred())
		preserved := &routev1.Route{}
		Expect(cli.Get(ctx, client.ObjectKeyFromObject(ownedRoute), preserved)).To(Succeed())
		Expect(preserved.OwnerReferences).To(Equal(ownedRoute.OwnerReferences))
		Expect(preserved.Spec).To(Equal(ownedRoute.Spec))
	})

	It("updates an owned Route in place after a NetworkPolicy collision is fixed", func(ctx SpecContext) {
		isvc := newISVC()
		ownedRoute := &routev1.Route{
			ObjectMeta: metav1.ObjectMeta{
				Name:      isvc.Name,
				Namespace: isvc.Namespace,
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: kservev1beta1.SchemeGroupVersion.String(),
					Kind:       constants.InferenceServiceKind,
					Name:       isvc.Name,
					UID:        isvc.UID,
					Controller: ptr.To(true),
				}},
			},
		}
		predictor := newService("raw-model-predictor", predictorNetworkPolicyComponent, corev1.ServicePort{
			Name: "http", Port: 80, TargetPort: intstr.FromInt32(8080),
		})
		collision := &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      rawNetworkPolicyName(isvc, predictorNetworkPolicyComponent),
				Namespace: isvc.Namespace,
			},
		}
		ownedRoute.UID = types.UID("existing-route-uid")
		cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(isvc, ownedRoute, predictor, collision).Build()
		reconciler := NewKServeRawInferenceServiceReconciler(cli)

		Expect(reconciler.Reconcile(ctx, log.Log, isvc)).To(HaveOccurred())
		preserved := &routev1.Route{}
		Expect(cli.Get(ctx, client.ObjectKeyFromObject(ownedRoute), preserved)).To(Succeed())
		Expect(preserved.UID).To(Equal(ownedRoute.UID))
		Expect(preserved.Spec).To(Equal(ownedRoute.Spec))

		Expect(cli.Delete(ctx, collision)).To(Succeed())
		Expect(reconciler.Reconcile(ctx, log.Log, isvc)).To(Succeed())
		updated := &routev1.Route{}
		Expect(cli.Get(ctx, client.ObjectKeyFromObject(ownedRoute), updated)).To(Succeed())
		Expect(updated.UID).To(Equal(ownedRoute.UID))
		Expect(updated.Spec.To.Name).To(Equal(predictor.Name))
		Expect(isOwnedByInferenceServiceOwnerReferences(updated.OwnerReferences, isvc)).To(BeTrue())
	})

	It("continues every independent sub-reconciler and aggregates its errors after a policy failure", func(ctx SpecContext) {
		isvc := newISVC()
		policyErr := errors.New("service listing failed")
		tailErr := errors.New("independent reconciliation failed")
		cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(isvc).WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if _, ok := list.(*corev1.ServiceList); ok {
					return policyErr
				}
				return c.List(ctx, list, opts...)
			},
		}).Build()
		reconciler := NewKServeRawInferenceServiceReconciler(cli)
		reconciler.subResourceReconcilers = reconciler.subResourceReconcilers[:3]
		var calls []string
		for _, name := range []string{"metrics", "service-monitor", "dashboard", "keda"} {
			reconciler.subResourceReconcilers = append(reconciler.subResourceReconcilers, &recordingSubReconciler{
				reconcile: func() error {
					calls = append(calls, name)
					if name == "metrics" {
						return tailErr
					}
					return nil
				},
			})
		}
		err := reconciler.Reconcile(ctx, log.Log, isvc)
		Expect(errors.Is(err, policyErr)).To(BeTrue())
		Expect(errors.Is(err, tailErr)).To(BeTrue())
		Expect(calls).To(Equal([]string{"metrics", "service-monitor", "dashboard", "keda"}))
	})

	DescribeTable("gates only Route writes on policy API failures", func(ctx SpecContext, failureCase string) {
		for _, existingRoute := range []bool{false, true} {
			failure := failureCase
			isvc := newISVC()
			predictor := newService("raw-model-predictor", "predictor", corev1.ServicePort{Name: "http", Port: 80, TargetPort: intstr.FromInt32(8080)})
			base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(isvc, predictor).Build()
			policyReconciler := NewKserveRawNetworkPolicyReconciler(base)
			if failure != "create" {
				Expect(policyReconciler.Reconcile(ctx, log.Log, isvc)).To(Succeed())
			}
			if failure == policyUpdateFailure {
				policy := &networkingv1.NetworkPolicy{}
				Expect(base.Get(ctx, client.ObjectKey{Name: rawNetworkPolicyName(isvc, predictorNetworkPolicyComponent), Namespace: isvc.Namespace}, policy)).To(Succeed())
				policy.Spec.Ingress = nil
				Expect(base.Update(ctx, policy)).To(Succeed())
			}
			route := &routev1.Route{ObjectMeta: metav1.ObjectMeta{
				Name: isvc.Name, Namespace: isvc.Namespace, UID: types.UID("existing-route-uid"),
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: kservev1beta1.SchemeGroupVersion.String(), Kind: constants.InferenceServiceKind,
					Name: isvc.Name, UID: isvc.UID, Controller: ptr.To(true),
				}},
			}, Spec: routev1.RouteSpec{To: routev1.RouteTargetReference{Kind: "Service", Name: "old-backend"}}}
			if existingRoute {
				Expect(base.Create(ctx, route)).To(Succeed())
			}
			policyErr := error(apierrors.NewTimeoutError("policy API timeout", 1))
			if failure == policyUpdateFailure {
				policyErr = apierrors.NewConflict(schema.GroupResource{Group: "networking.k8s.io", Resource: "networkpolicies"}, "policy", errors.New("stale resource version"))
			}
			routeWrites := 0
			cli := interceptor.NewClient(base, interceptor.Funcs{
				List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					if _, ok := list.(*corev1.ServiceList); ok && failure == "services" {
						return policyErr
					}
					if _, ok := list.(*kservev1alpha1.InferenceGraphList); ok && failure == "graphs" {
						return policyErr
					}
					return c.List(ctx, list, opts...)
				},
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*networkingv1.NetworkPolicy); ok && failure == "get" {
						return policyErr
					}
					return c.Get(ctx, key, obj, opts...)
				},
				Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					if _, ok := obj.(*networkingv1.NetworkPolicy); ok && failure == "create" {
						return policyErr
					}
					if _, ok := obj.(*routev1.Route); ok {
						routeWrites++
					}
					return c.Create(ctx, obj, opts...)
				},
				Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
					if _, ok := obj.(*networkingv1.NetworkPolicy); ok && failure == policyUpdateFailure {
						return policyErr
					}
					if _, ok := obj.(*routev1.Route); ok {
						routeWrites++
					}
					return c.Update(ctx, obj, opts...)
				},
				Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					if _, ok := obj.(*routev1.Route); ok {
						routeWrites++
					}
					return c.Delete(ctx, obj, opts...)
				},
			})
			reconciler := &KserveRawInferenceServiceReconciler{subResourceReconcilers: []SubResourceReconciler{
				NewKserveRawNetworkPolicyReconciler(cli), NewKserveRawRouteReconciler(cli),
			}}
			Expect(errors.Is(reconciler.Reconcile(ctx, log.Log, isvc), policyErr)).To(BeTrue())
			Expect(routeWrites).To(BeZero())
			actual := &routev1.Route{}
			err := base.Get(ctx, client.ObjectKeyFromObject(route), actual)
			if existingRoute {
				Expect(err).NotTo(HaveOccurred())
				Expect(actual.UID).To(Equal(route.UID))
				Expect(actual.Spec).To(Equal(route.Spec))
			} else {
				Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}

			// Clear the API failure and allow the normal Route delta to converge.
			failure = ""
			Expect(reconciler.Reconcile(ctx, log.Log, isvc)).To(Succeed())
			Expect(routeWrites).To(Equal(1))
			Expect(base.Get(ctx, client.ObjectKeyFromObject(route), actual)).To(Succeed())
			Expect(actual.Spec.To.Name).To(Equal(predictor.Name))
			if existingRoute {
				Expect(actual.UID).To(Equal(route.UID))
			}
		}
	},
		Entry("when Service listing times out", "services"),
		Entry("when graph listing times out", "graphs"),
		Entry("when policy lookup times out", "get"),
		Entry("when policy update conflicts", policyUpdateFailure),
		Entry("when policy creation times out", "create"),
	)

	It("cleans up only managed policies with an InferenceService owner", func(ctx SpecContext) {
		isvc := newISVC()
		owned := &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "owned-orphan",
				Namespace: isvc.Namespace,
				Labels: map[string]string{
					managedByLabel:                managedByValue,
					"app.kubernetes.io/component": rawNetworkPolicyManagedComponent,
				},
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: kservev1beta1.SchemeGroupVersion.String(),
					Kind:       constants.InferenceServiceKind,
					Name:       isvc.Name,
					UID:        isvc.UID,
					Controller: ptr.To(true),
				}},
			},
		}
		unowned := owned.DeepCopy()
		unowned.Name = "unowned-user-policy"
		unowned.OwnerReferences = nil
		cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(owned, unowned).Build()
		Expect(NewKserveRawNetworkPolicyReconciler(cli).Cleanup(ctx, log.Log, isvc.Namespace)).To(Succeed())

		err := cli.Get(ctx, client.ObjectKeyFromObject(owned), &networkingv1.NetworkPolicy{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
		Expect(cli.Get(ctx, client.ObjectKeyFromObject(unowned), &networkingv1.NetworkPolicy{})).To(Succeed())
	})
})

func policyPortStrings(ports []networkingv1.NetworkPolicyPort) []string {
	result := make([]string, 0, len(ports))
	for _, port := range ports {
		protocol := corev1.ProtocolTCP
		if port.Protocol != nil {
			protocol = *port.Protocol
		}
		portString := port.Port.String()
		if port.EndPort != nil {
			portString += "-" + strconv.Itoa(int(*port.EndPort))
		}
		result = append(result, string(protocol)+":"+portString)
	}
	return result
}
