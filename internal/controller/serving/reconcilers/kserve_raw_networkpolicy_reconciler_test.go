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
	"strconv"
	"strings"

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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	controllerutils "github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

var _ = Describe("KserveRawNetworkPolicyReconciler", func() {
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

	It("removes an existing Route when policy reconciliation fails after exposure is disabled", func(ctx SpecContext) {
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
		collision := &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      rawNetworkPolicyName(isvc, predictorNetworkPolicyComponent),
				Namespace: isvc.Namespace,
			},
		}
		cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(isvc, route, collision).Build()
		delete(isvc.Labels, constants.KserveNetworkVisibility)

		Expect(NewKServeRawInferenceServiceReconciler(cli).Reconcile(ctx, log.Log, isvc)).To(HaveOccurred())
		err := cli.Get(ctx, client.ObjectKey{Name: isvc.Name, Namespace: isvc.Namespace}, &routev1.Route{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	It("fails closed by deleting only an owned Route when policy reconciliation fails", func(ctx SpecContext) {
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
		err := cli.Get(ctx, client.ObjectKey{Name: isvc.Name, Namespace: isvc.Namespace}, &routev1.Route{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())

		unownedRoute := ownedRoute.DeepCopy()
		unownedRoute.OwnerReferences = nil
		unownedRoute.ResourceVersion = ""
		unownedRoute.UID = ""
		Expect(cli.Create(ctx, unownedRoute)).To(Succeed())
		Expect(reconciler.Reconcile(ctx, log.Log, isvc)).To(HaveOccurred())
		Expect(cli.Get(ctx, client.ObjectKey{Name: isvc.Name, Namespace: isvc.Namespace}, &routev1.Route{})).To(Succeed())
	})

	It("recreates an owned Route after a NetworkPolicy collision is fixed", func(ctx SpecContext) {
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
		cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(isvc, ownedRoute, predictor, collision).Build()
		reconciler := NewKServeRawInferenceServiceReconciler(cli)

		Expect(reconciler.Reconcile(ctx, log.Log, isvc)).To(HaveOccurred())
		err := cli.Get(ctx, client.ObjectKey{Name: isvc.Name, Namespace: isvc.Namespace}, &routev1.Route{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())

		Expect(cli.Delete(ctx, collision)).To(Succeed())
		Expect(reconciler.Reconcile(ctx, log.Log, isvc)).To(Succeed())
		recreated := &routev1.Route{}
		Expect(cli.Get(ctx, client.ObjectKey{Name: isvc.Name, Namespace: isvc.Namespace}, recreated)).To(Succeed())
		Expect(isOwnedByInferenceServiceOwnerReferences(recreated.OwnerReferences, isvc)).To(BeTrue())
	})

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
