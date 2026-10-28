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
	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	kserveconstants "github.com/kserve/kserve/pkg/constants"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

var _ = Describe("KserveRawInferenceGraphNetworkPolicyReconciler", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		controllerutils.RegisterSchemes(scheme)
	})

	newGraph := func() *kservev1alpha1.InferenceGraph {
		return &kservev1alpha1.InferenceGraph{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "routing-graph",
				Namespace: "models",
				UID:       types.UID("routing-graph-uid"),
				Labels: map[string]string{
					kserveconstants.NetworkVisibility: "exposed",
				},
			},
		}
	}

	It("creates and repairs the exposed graph policy", func(ctx SpecContext) {
		graph := newGraph()
		cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(graph).Build()
		reconciler := NewKserveRawInferenceGraphNetworkPolicyReconciler(cli)

		Expect(reconciler.Reconcile(ctx, log.Log, graph)).To(Succeed())
		policy := &networkingv1.NetworkPolicy{}
		key := client.ObjectKey{Name: inferenceGraphNetworkPolicyName(graph), Namespace: graph.Namespace}
		Expect(cli.Get(ctx, key, policy)).To(Succeed())
		Expect(policy.Spec.PodSelector.MatchLabels).To(Equal(map[string]string{
			kserveconstants.InferenceGraphLabel: graph.Name,
		}))
		Expect(policy.Spec.PolicyTypes).To(ConsistOf(networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress))
		Expect(policy.Spec.Egress).To(HaveLen(1))
		Expect(policy.Spec.Ingress).To(HaveLen(1))
		Expect(policy.Spec.Ingress[0].From[0].NamespaceSelector.MatchLabels).To(Equal(map[string]string{
			openshiftIngressPolicyGroupLabel: "",
		}))
		Expect(policyPortStrings(policy.Spec.Ingress[0].Ports)).To(Equal([]string{"TCP:8080"}))
		Expect(policy.OwnerReferences).To(ConsistOf(metav1.OwnerReference{
			APIVersion:         kservev1alpha1.SchemeGroupVersion.String(),
			Kind:               "InferenceGraph",
			Name:               graph.Name,
			UID:                graph.UID,
			Controller:         ptr.To(true),
			BlockOwnerDeletion: ptr.To(true),
		}))

		policy.Spec.Ingress = nil
		Expect(cli.Update(ctx, policy)).To(Succeed())
		Expect(reconciler.Reconcile(ctx, log.Log, graph)).To(Succeed())
		Expect(cli.Get(ctx, key, policy)).To(Succeed())
		Expect(policy.Spec.Ingress).To(HaveLen(1))
	})

	It("switches cluster-local ingress to the graph-specific caller label", func(ctx SpecContext) {
		graph := newGraph()
		graph.Labels[kserveconstants.NetworkVisibility] = kserveconstants.ClusterLocalVisibility
		cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(graph).Build()
		reconciler := NewKserveRawInferenceGraphNetworkPolicyReconciler(cli)

		Expect(reconciler.Reconcile(ctx, log.Log, graph)).To(Succeed())
		policy := &networkingv1.NetworkPolicy{}
		Expect(cli.Get(ctx, client.ObjectKey{Name: inferenceGraphNetworkPolicyName(graph), Namespace: graph.Namespace}, policy)).To(Succeed())
		peer := policy.Spec.Ingress[0].From[0]
		Expect(peer.NamespaceSelector).To(BeNil())
		Expect(peer.PodSelector.MatchLabels).To(Equal(map[string]string{
			inferenceGraphCallerLabel: graph.Name,
		}))
		Expect(policyPortStrings(policy.Spec.Ingress[0].Ports)).To(Equal([]string{"TCP:8080"}))

		Expect(reconciler.Delete(ctx, log.Log, graph)).To(Succeed())
		err := cli.Get(ctx, client.ObjectKey{Name: inferenceGraphNetworkPolicyName(graph), Namespace: graph.Namespace}, &networkingv1.NetworkPolicy{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	It("does not take over an unowned policy", func(ctx SpecContext) {
		graph := newGraph()
		collision := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{
			Name:      inferenceGraphNetworkPolicyName(graph),
			Namespace: graph.Namespace,
		}}
		cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(graph, collision).Build()

		err := NewKserveRawInferenceGraphNetworkPolicyReconciler(cli).Reconcile(ctx, log.Log, graph)
		Expect(err).To(MatchError(ContainSubstring("refusing to overwrite")))
	})

	It("recognizes only raw graph deployment modes", func() {
		graph := newGraph()
		Expect(IsRawInferenceGraph(graph)).To(BeTrue())
		graph.Annotations = map[string]string{kserveconstants.DeploymentMode: string(kserveconstants.LegacyRawDeployment)}
		Expect(IsRawInferenceGraph(graph)).To(BeTrue())
		graph.Annotations[kserveconstants.DeploymentMode] = string(kserveconstants.Knative)
		Expect(IsRawInferenceGraph(graph)).To(BeFalse())
		graph.Status.DeploymentMode = string(kserveconstants.Standard)
		Expect(IsRawInferenceGraph(graph)).To(BeTrue())
		graph.Status.DeploymentMode = string(kserveconstants.ModelMeshDeployment)
		Expect(IsRawInferenceGraph(graph)).To(BeFalse())

		isvc := &kservev1beta1.InferenceService{}
		Expect(IsRawInferenceService(isvc)).To(BeTrue())
		isvc.Status.DeploymentMode = string(kserveconstants.Knative)
		Expect(IsRawInferenceService(isvc)).To(BeFalse())
	})

	It("accepts only internal URLs for known graph targets", func() {
		service := corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "raw-model-predictor", Namespace: "models"}, Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 80, TargetPort: intstr.FromInt32(8080)}}}}
		Expect(graphServiceURLMatches("http://raw-model-predictor.models.svc.cluster.local", "models", []corev1.Service{service})).To(BeTrue())
		Expect(graphServiceURLMatches("http://raw-model-predictor.models.svc.cluster.local:80", "models", []corev1.Service{service})).To(BeTrue())
		Expect(graphServiceURLMatches("http://raw-model-predictor.models.svc.cluster.local:8080", "models", []corev1.Service{service})).To(BeFalse())
		service.Spec.ClusterIP = corev1.ClusterIPNone
		Expect(graphServiceURLMatches("http://raw-model-predictor.models.svc.cluster.local:8080", "models", []corev1.Service{service})).To(BeTrue())
		Expect(graphServiceURLMatches("http://example.com", "models", []corev1.Service{service})).To(BeFalse())
		Expect(graphServiceURLMatches("not a URL", "models", []corev1.Service{service})).To(BeFalse())
	})

	It("adds and removes only verified graph peers on the ISVC policy", func(ctx SpecContext) {
		isvc := &kservev1beta1.InferenceService{ObjectMeta: metav1.ObjectMeta{
			Name: "raw-model", Namespace: "models", UID: types.UID("raw-model-uid"),
		}}
		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "raw-model-predictor",
				Namespace: "models",
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: kservev1beta1.SchemeGroupVersion.String(),
					Kind:       constants.InferenceServiceKind,
					Name:       isvc.Name,
					UID:        isvc.UID,
					Controller: ptr.To(true),
				}},
				Labels: map[string]string{
					constants.KserveGroupAnnotation:        isvc.Name,
					kserveconstants.KServiceComponentLabel: "predictor",
				},
			},
			Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{
				Name: "http", Port: 80, TargetPort: intstr.FromInt32(8080),
			}}},
		}
		graph := newGraph()
		graph.Spec.Nodes = map[string]kservev1alpha1.InferenceRouter{
			"root": {Steps: []kservev1alpha1.InferenceStep{{InferenceTarget: kservev1alpha1.InferenceTarget{
				ServiceName: "raw-model", ServiceURL: "https://external.example.com",
			}}}},
		}
		cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(isvc, service, graph).Build()
		reconciler := NewKserveRawNetworkPolicyReconciler(cli)

		Expect(reconciler.Reconcile(ctx, log.Log, isvc)).To(Succeed())
		policy := &networkingv1.NetworkPolicy{}
		key := client.ObjectKey{Name: rawNetworkPolicyName(isvc, predictorNetworkPolicyComponent), Namespace: isvc.Namespace}
		Expect(cli.Get(ctx, key, policy)).To(Succeed())
		Expect(policy.Spec.Ingress).To(BeEmpty())

		graph.Spec.Nodes["root"].Steps[0].ServiceURL = "http://raw-model-predictor.models.svc.cluster.local"
		Expect(cli.Update(ctx, graph)).To(Succeed())
		Expect(reconciler.Reconcile(ctx, log.Log, isvc)).To(Succeed())
		Expect(cli.Get(ctx, key, policy)).To(Succeed())
		Expect(policy.Spec.Ingress).To(HaveLen(1))
		Expect(policy.Spec.Ingress[0].From[0].PodSelector.MatchLabels).To(Equal(map[string]string{
			kserveconstants.InferenceGraphLabel: graph.Name,
		}))
		Expect(policyPortStrings(policy.Spec.Ingress[0].Ports)).To(Equal([]string{"TCP:8080"}))

		// KServe resolves a named ServiceName to the ISVC's internal Address when
		// ServiceURL is omitted, including when the ISVC also has an exposed Route.
		graph.Spec.Nodes["root"].Steps[0].ServiceURL = ""
		Expect(cli.Update(ctx, graph)).To(Succeed())
		Expect(reconciler.Reconcile(ctx, log.Log, isvc)).To(Succeed())
		Expect(cli.Get(ctx, key, policy)).To(Succeed())
		Expect(policy.Spec.Ingress).To(HaveLen(1))

		graph.Spec.Nodes["root"].Steps[0].ServiceURL = "https://external.example.com"
		Expect(cli.Update(ctx, graph)).To(Succeed())
		Expect(reconciler.Reconcile(ctx, log.Log, isvc)).To(Succeed())
		Expect(cli.Get(ctx, key, policy)).To(Succeed())
		Expect(policy.Spec.Ingress).To(BeEmpty())
	})

	It("uses the transformer HTTPS target port for a blank ServiceURL", func(ctx SpecContext) {
		isvc := &kservev1beta1.InferenceService{ObjectMeta: metav1.ObjectMeta{
			Name: "raw-model", Namespace: "models", UID: types.UID("raw-model-uid"),
			Annotations: map[string]string{constants.EnableAuthODHAnnotation: "true"},
		}, Spec: kservev1beta1.InferenceServiceSpec{
			Transformer: &kservev1beta1.TransformerSpec{},
		}}
		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "raw-model-transformer",
				Namespace: "models",
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: kservev1beta1.SchemeGroupVersion.String(),
					Kind:       constants.InferenceServiceKind,
					Name:       isvc.Name,
					UID:        isvc.UID,
					Controller: ptr.To(true),
				}},
				Labels: map[string]string{
					constants.KserveGroupAnnotation:        isvc.Name,
					kserveconstants.KServiceComponentLabel: transformerNetworkPolicyComponent,
				},
			},
			Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{
				Name: "https", Port: 443, TargetPort: intstr.FromInt32(8443),
			}}},
		}
		graph := newGraph()
		graph.Spec.Nodes = map[string]kservev1alpha1.InferenceRouter{
			"root": {Steps: []kservev1alpha1.InferenceStep{{InferenceTarget: kservev1alpha1.InferenceTarget{
				ServiceName: isvc.Name,
			}}}},
		}
		cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(isvc, service, graph).Build()

		Expect(NewKserveRawNetworkPolicyReconciler(cli).Reconcile(ctx, log.Log, isvc)).To(Succeed())
		policy := &networkingv1.NetworkPolicy{}
		Expect(cli.Get(ctx, client.ObjectKey{
			Name:      rawNetworkPolicyName(isvc, transformerNetworkPolicyComponent),
			Namespace: isvc.Namespace,
		}, policy)).To(Succeed())
		Expect(policy.Spec.Ingress).To(HaveLen(1))
		Expect(policy.Spec.Ingress[0].From[0].PodSelector.MatchLabels).To(Equal(map[string]string{
			kserveconstants.InferenceGraphLabel: graph.Name,
		}))
		Expect(policyPortStrings(policy.Spec.Ingress[0].Ports)).To(Equal([]string{"TCP:8443"}))
	})
})
