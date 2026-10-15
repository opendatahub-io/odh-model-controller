/*
Copyright 2026.

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

package llm_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	kservellmisvc "github.com/kserve/kserve/pkg/controller/v1alpha2/llmisvc"
	kuadrantv1 "github.com/kuadrant/kuadrant-operator/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/events"
	"k8s.io/utils/ptr"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	"knative.dev/pkg/network"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	llmcontroller "github.com/opendatahub-io/odh-model-controller/internal/controller/serving/llm"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/serving/llm/fixture"
	testutils "github.com/opendatahub-io/odh-model-controller/test/utils"
)

var gridGVK = schema.GroupVersionKind{Group: "grid.praxis.fast", Version: "v1alpha1", Kind: "InferenceProvider"}

// Force the provider create path to fail while retaining real API-server reads
// and writes for the source, Gateway, credentials, and RBAC resources.
type gridProviderFailureClient struct{ client.Client }

func (c *gridProviderFailureClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if obj.GetObjectKind().GroupVersionKind() == gridGVK {
		return apierrors.NewNotFound(schema.GroupResource{Group: gridGVK.Group, Resource: "inferenceproviders"}, key.Name)
	}
	return c.Client.Get(ctx, key, obj, opts...)
}
func (c *gridProviderFailureClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if obj.GetObjectKind().GroupVersionKind() == gridGVK {
		return errors.New("provider creation denied")
	}
	return c.Client.Create(ctx, obj, opts...)
}

type gridWarningRecorder struct {
	*events.FakeRecorder
	regarding runtime.Object
}

func (r *gridWarningRecorder) Eventf(regarding runtime.Object, related runtime.Object, eventType, reason, action, note string, args ...interface{}) {
	r.regarding = regarding
	r.FakeRecorder.Eventf(regarding, related, eventType, reason, action, note, args...)
}

var _ = Describe("Grid provider reconciliation", Ordered, func() {
	var ns *corev1.Namespace
	var svc *kservev1alpha2.LLMInferenceService
	var gateway *gatewayv1.Gateway
	var route *gatewayv1.HTTPRoute
	var endpoint string
	var internalHost string

	BeforeAll(func(ctx SpecContext) {
		Expect(envTest.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "grid-test-identity"}})).To(Succeed())
		sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "grid-client", Namespace: "grid-test-identity"}}
		Expect(envTest.Create(ctx, sa)).To(Succeed())
		// envtest has no token controller, so seed its populated output.
		Expect(envTest.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "grid-token", Namespace: sa.Namespace, Annotations: map[string]string{
				corev1.ServiceAccountNameKey: sa.Name, corev1.ServiceAccountUIDKey: string(sa.UID),
			}},
			Type: corev1.SecretTypeServiceAccountToken, Data: map[string][]byte{"token": []byte("test-token")},
		})).To(Succeed())
		secondSA := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "grid-client-second", Namespace: "grid-test-identity"}}
		Expect(envTest.Create(ctx, secondSA)).To(Succeed())
		Expect(envTest.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "grid-token-second", Namespace: secondSA.Namespace, Annotations: map[string]string{corev1.ServiceAccountNameKey: secondSA.Name, corev1.ServiceAccountUIDKey: string(secondSA.UID)}}, Type: corev1.SecretTypeServiceAccountToken, Data: map[string][]byte{"token": []byte("second-token")}})).To(Succeed())
	})

	BeforeEach(func(ctx SpecContext) {
		ns = testutils.Namespaces.Create(ctx, envTest.Client)
		ns.Labels = map[string]string{}
		Expect(envTest.Update(ctx, ns)).To(Succeed())
		gateway = &gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: "grid-gateway", Namespace: ns.Name}, Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "grid-test", Listeners: []gatewayv1.Listener{{Name: "http", Protocol: gatewayv1.HTTPProtocolType, Port: 80}},
		}}
		Expect(envTest.Create(ctx, gateway)).To(Succeed())
		gateway.Status.Addresses = []gatewayv1.GatewayStatusAddress{{Type: ptr.To(gatewayv1.HostnameAddressType), Value: "grid.example.com"}}
		Expect(envTest.Status().Update(ctx, gateway)).To(Succeed())
		internalHost = network.GetServiceHostname("grid-gateway", ns.Name)
		parent := gatewayv1.ParentReference{Name: gatewayv1.ObjectName(gateway.Name)}
		route = &gatewayv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Name: "grid-route", Namespace: ns.Name}, Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{ParentRefs: []gatewayv1.ParentReference{parent}},
			Rules:           []gatewayv1.HTTPRouteRule{{Matches: []gatewayv1.HTTPRouteMatch{{Path: &gatewayv1.HTTPPathMatch{Type: ptr.To(gatewayv1.PathMatchPathPrefix), Value: ptr.To("/" + ns.Name + "/grid-model")}}}}},
		}}
		Expect(envTest.Create(ctx, route)).To(Succeed())
		route.Status.Parents = []gatewayv1.RouteParentStatus{{ParentRef: parent, ControllerName: "example.com/grid-test", Conditions: []metav1.Condition{
			{Type: "Accepted", Status: metav1.ConditionTrue, ObservedGeneration: route.Generation, Reason: "Accepted", LastTransitionTime: metav1.Now()},
			{Type: "ResolvedRefs", Status: metav1.ConditionTrue, ObservedGeneration: route.Generation, Reason: "ResolvedRefs", LastTransitionTime: metav1.Now()},
		}}}
		Expect(envTest.Status().Update(ctx, route)).To(Succeed())
		svc = fixture.LLMInferenceService("grid-model", fixture.WithModelURI("hf://test/model"), fixture.WithModelName("test/model"),
			fixture.WithGatewayRefs(fixture.LLMGatewayRef(gateway.Name, ns.Name)), fixture.WithHTTPRouteRefs(fixture.HTTPRouteRef(route.Name)))
		svc.Namespace = ns.Name
		// Opt-in happens in each test, allowing negative assertions first.
		Expect(envTest.Create(ctx, svc)).To(Succeed())
		endpoint = fmt.Sprintf("http://%s/%s/%s", internalHost, ns.Name, svc.Name)
		address, err := apis.ParseURL(endpoint)
		Expect(err).NotTo(HaveOccurred())
		origin := gatewayv1.ObjectReference{Group: gatewayv1.GroupName, Kind: "Gateway", Name: gatewayv1.ObjectName(gateway.Name), Namespace: ptr.To(gatewayv1.Namespace(ns.Name))}
		Eventually(func() error {
			if err := envTest.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
				return err
			}
			svc.Status.Router = &kservev1alpha2.RouterStatus{Gateways: []kservev1alpha2.ObservedGateway{{ObjectReference: origin,
				HTTPRoutes: []gatewayv1.ObjectReference{{Group: gatewayv1.GroupName, Kind: "HTTPRoute", Name: gatewayv1.ObjectName(route.Name)}},
			}}}
			svc.Status.URL = address
			svc.Status.Addresses = []kservev1alpha2.SourcedAddress{{Addressable: duckv1.Addressable{Name: ptr.To("gateway-internal"), URL: address}, Origin: &origin}}
			return envTest.Status().Update(ctx, svc)
		}).Should(Succeed())
	})

	providers := func(ctx context.Context) []unstructured.Unstructured {
		list := &unstructured.UnstructuredList{}
		list.SetGroupVersionKind(gridGVK.GroupVersion().WithKind("InferenceProviderList"))
		Expect(envTest.List(ctx, list)).To(Succeed())
		var matched []unstructured.Unstructured
		for _, provider := range list.Items {
			if provider.GetAnnotations()["grid.praxis.fast/source-namespace"] == ns.Name {
				matched = append(matched, provider)
			}
		}
		return matched
	}
	updateService := func(ctx context.Context, change func(*kservev1alpha2.LLMInferenceService)) {
		Eventually(func() error {
			if err := envTest.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
				return err
			}
			if svc.Annotations == nil {
				svc.Annotations = map[string]string{}
			}
			change(svc)
			return envTest.Update(ctx, svc)
		}).Should(Succeed())
	}
	optIn := func(ctx context.Context) {
		Expect(envTest.Get(ctx, client.ObjectKeyFromObject(ns), ns)).To(Succeed())
		ns.Labels = map[string]string{llmcontroller.GridAllowedLabel: "true"}
		gateway.Annotations = map[string]string{llmcontroller.GridServingAnnotation: `{"networkName":"grid-test","namespace":"grid-test-identity","serviceAccount":"grid-client","tokenSecret":"grid-token","siteLabels":"grid.praxis.fast/provider-site=test"}`}
		Expect(envTest.Update(ctx, ns)).To(Succeed())
		Expect(envTest.Update(ctx, gateway)).To(Succeed())
	}
	optOut := func(ctx SpecContext) {
		Expect(envTest.Get(ctx, client.ObjectKeyFromObject(gateway), gateway)).To(Succeed())
		delete(gateway.Annotations, llmcontroller.GridServingAnnotation)
		Expect(envTest.Update(ctx, gateway)).To(Succeed())
	}
	waitProvider := func(ctx context.Context) *unstructured.Unstructured {
		Eventually(func() []unstructured.Unstructured { return providers(ctx) }).Should(HaveLen(1))
		provider := providers(ctx)[0]
		return &provider
	}

	AfterEach(func(ctx SpecContext) {
		if err := envTest.Get(ctx, client.ObjectKeyFromObject(svc), svc); err == nil {
			Expect(client.IgnoreNotFound(envTest.Delete(ctx, svc))).To(Succeed())
			Eventually(func() bool {
				return apierrors.IsNotFound(envTest.Get(ctx, client.ObjectKeyFromObject(svc), &kservev1alpha2.LLMInferenceService{}))
			}).Should(BeTrue())
		}
		Eventually(func() []unstructured.Unstructured { return providers(ctx) }).Should(BeEmpty())
		Expect(client.IgnoreNotFound(envTest.Delete(ctx, route))).To(Succeed())
		Expect(client.IgnoreNotFound(envTest.Delete(ctx, gateway))).To(Succeed())
	})

	It("requires explicit opt-in and creates model-scoped auth attachments", func(ctx SpecContext) {
		Consistently(func() []unstructured.Unstructured { return providers(ctx) }, "500ms").Should(BeEmpty())
		optIn(ctx)
		provider := waitProvider(ctx)
		Expect(provider.GetNamespace()).To(BeEmpty())
		Expect(provider.GetOwnerReferences()).To(BeEmpty())
		Expect(provider.Object["spec"]).To(HaveKeyWithValue("endpoint", endpoint))
		Expect(provider.Object["spec"]).NotTo(HaveKey("gatewayRef"))
		auth, _, err := unstructured.NestedMap(provider.Object, "spec", "auth")
		Expect(err).NotTo(HaveOccurred())
		Expect(auth).To(Equal(map[string]interface{}{"strategy": "bearer_token", "manual": false, "secretRef": map[string]interface{}{"name": "grid-token", "namespace": "grid-test-identity", "key": "token"}}))
		role := &rbacv1.Role{}
		Expect(envTest.Get(ctx, client.ObjectKey{Namespace: ns.Name, Name: provider.GetName()}, role)).To(Succeed())
		Expect(role.Rules).To(Equal([]rbacv1.PolicyRule{{APIGroups: []string{"serving.kserve.io"}, Resources: []string{"llminferenceservices"}, ResourceNames: []string{svc.Name}, Verbs: []string{"get"}}}))
		binding := &rbacv1.RoleBinding{}
		Expect(envTest.Get(ctx, client.ObjectKeyFromObject(role), binding)).To(Succeed())
		Expect(binding.Subjects).To(Equal([]rbacv1.Subject{{Kind: "ServiceAccount", Name: "grid-client", Namespace: "grid-test-identity"}}))
	})

	It("emits a Warning on the source when provider creation fails", func(ctx SpecContext) {
		optIn(ctx)
		waitProvider(ctx)
		recorder := &gridWarningRecorder{FakeRecorder: events.NewFakeRecorder(20)}
		reconciler := &llmcontroller.GridInferenceProviderReconciler{Client: &gridProviderFailureClient{Client: envTest.Client}, APIReader: envTest.Client, Recorder: recorder}
		Eventually(func() error {
			_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(svc)})
			return err
		}).Should(MatchError(ContainSubstring("provider creation denied")))
		Eventually(recorder.Events).Should(Receive(And(ContainSubstring("Warning InferenceProviderReconcileFailed"), ContainSubstring(gateway.Namespace+"/"+gateway.Name), ContainSubstring("provider creation denied"))))
		Expect(recorder.regarding).To(BeAssignableToTypeOf(&kservev1alpha2.LLMInferenceService{}))
		Expect(recorder.regarding.(*kservev1alpha2.LLMInferenceService).UID).To(Equal(svc.UID))
	})

	DescribeTable("publishes separate providers for handy refs and HTTPRoute parentRefs", func(ctx SpecContext, inline bool) {
		infra := testutils.Namespaces.Create(ctx, envTest.Client)
		second := &gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: "second-grid-gateway", Namespace: infra.Name, Annotations: map[string]string{llmcontroller.GridServingAnnotation: `{"networkName":"grid-second","namespace":"grid-test-identity","serviceAccount":"grid-client-second","tokenSecret":"grid-token-second","siteLabels":"grid.praxis.fast/provider-site=second"}`}}, Spec: gatewayv1.GatewaySpec{GatewayClassName: "grid-test", Listeners: []gatewayv1.Listener{{Name: "http", Protocol: gatewayv1.HTTPProtocolType, Port: 80, AllowedRoutes: &gatewayv1.AllowedRoutes{Namespaces: &gatewayv1.RouteNamespaces{From: ptr.To(gatewayv1.NamespacesFromAll)}}}}}}
		Expect(envTest.Create(ctx, second)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) { Expect(client.IgnoreNotFound(envTest.Delete(ctx, second))).To(Succeed()) })
		parents := []gatewayv1.ParentReference{{Name: gatewayv1.ObjectName(gateway.Name)}, {Name: gatewayv1.ObjectName(second.Name), Namespace: ptr.To(gatewayv1.Namespace(second.Namespace))}}
		Expect(envTest.Get(ctx, client.ObjectKeyFromObject(route), route)).To(Succeed())
		route.Spec.ParentRefs = parents
		Expect(envTest.Update(ctx, route)).To(Succeed())
		if inline {
			updateService(ctx, func(s *kservev1alpha2.LLMInferenceService) {
				s.Spec.Router.Route.HTTP.Refs = nil
				s.Spec.Router.Route.HTTP.Spec = route.Spec.DeepCopy()
			})
		}
		secondEndpoint := fmt.Sprintf("http://%s/%s/%s", network.GetServiceHostname(second.Name, second.Namespace), ns.Name, svc.Name)
		secondURL, err := apis.ParseURL(secondEndpoint)
		Expect(err).NotTo(HaveOccurred())
		observed := kservellmisvc.BuildObservedGateways([]*gatewayv1.HTTPRoute{route})
		origin := gatewayv1.ObjectReference{Group: gatewayv1.GroupName, Kind: "Gateway", Name: gatewayv1.ObjectName(second.Name), Namespace: ptr.To(gatewayv1.Namespace(second.Namespace))}
		Eventually(func() error {
			if err := envTest.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
				return err
			}
			svc.Status.Router.Gateways = observed
			if len(svc.Status.Addresses) == 1 {
				svc.Status.Addresses = append(svc.Status.Addresses, kservev1alpha2.SourcedAddress{Addressable: duckv1.Addressable{Name: ptr.To("gateway-internal"), URL: secondURL}, Origin: &origin})
			}
			return envTest.Status().Update(ctx, svc)
		}).Should(Succeed())
		optIn(ctx)
		Eventually(func() []unstructured.Unstructured { return providers(ctx) }).Should(HaveLen(2))
		byGateway := map[string]unstructured.Unstructured{}
		for _, provider := range providers(ctx) {
			byGateway[provider.GetAnnotations()["grid.opendatahub.io/gateway-name"]] = provider
		}
		firstProvider := byGateway[gateway.Name]
		secondProvider := byGateway[second.Name]
		Expect(firstProvider.GetName()).NotTo(Equal(secondProvider.GetName()))
		Expect(firstProvider.Object["spec"]).To(HaveKeyWithValue("endpoint", endpoint))
		Expect(secondProvider.Object["spec"]).To(HaveKeyWithValue("endpoint", secondEndpoint))
		Expect(secondProvider.Object["spec"]).To(HaveKeyWithValue("gridNetworkRef", "grid-second"))
		secretRef, _, err := unstructured.NestedMap(secondProvider.Object, "spec", "auth", "secretRef")
		Expect(err).NotTo(HaveOccurred())
		Expect(secretRef).To(HaveKeyWithValue("name", "grid-token-second"))
		// Provider publication follows KServe status independently of Gateway auth discovery.
		Expect(apierrors.IsNotFound(envTest.Get(ctx, client.ObjectKey{Namespace: second.Namespace, Name: constants.GetAuthPolicyName(second.Name)}, &kuadrantv1.AuthPolicy{}))).To(BeTrue())
		updateService(ctx, func(s *kservev1alpha2.LLMInferenceService) {
			s.Spec.Router.Gateway.Refs = append(s.Spec.Router.Gateway.Refs, fixture.LLMGatewayRef(second.Name, second.Namespace))
		})
		for _, entry := range []struct {
			gateway  *gatewayv1.Gateway
			provider unstructured.Unstructured
			account  string
		}{{gateway, firstProvider, "grid-client"}, {second, secondProvider, "grid-client-second"}} {
			binding := &rbacv1.RoleBinding{}
			Expect(envTest.Get(ctx, client.ObjectKey{Namespace: ns.Name, Name: entry.provider.GetName()}, binding)).To(Succeed())
			Expect(binding.Subjects).To(Equal([]rbacv1.Subject{{Kind: "ServiceAccount", Namespace: "grid-test-identity", Name: entry.account}}))
			Eventually(func() string {
				policy := &kuadrantv1.AuthPolicy{}
				if err := envTest.Get(ctx, client.ObjectKey{Namespace: entry.gateway.Namespace, Name: constants.GetAuthPolicyName(entry.gateway.Name)}, policy); err != nil {
					return ""
				}
				if policy.Spec.AuthScheme == nil {
					return ""
				}
				return string(policy.Spec.AuthScheme.Authentication["kubernetes-user"].Overrides["fairness"].Expression)
			}).Should(ContainSubstring("system:serviceaccount:grid-test-identity:" + entry.account + "\""))
		}
		// Revoking one Gateway leaves the other provider and its model access intact.
		Expect(envTest.Get(ctx, client.ObjectKeyFromObject(second), second)).To(Succeed())
		delete(second.Annotations, llmcontroller.GridServingAnnotation)
		Expect(envTest.Update(ctx, second)).To(Succeed())
		Eventually(func() []unstructured.Unstructured { return providers(ctx) }).Should(HaveLen(1))
		Expect(waitProvider(ctx).GetName()).To(Equal(firstProvider.GetName()))
		Eventually(func() bool {
			return apierrors.IsNotFound(envTest.Get(ctx, client.ObjectKey{Namespace: ns.Name, Name: secondProvider.GetName()}, &rbacv1.RoleBinding{}))
		}).Should(BeTrue())
		Eventually(func() string {
			policy := &kuadrantv1.AuthPolicy{}
			if err := envTest.Get(ctx, client.ObjectKey{Namespace: second.Namespace, Name: constants.GetAuthPolicyName(second.Name)}, policy); err != nil {
				return ""
			}
			if policy.Spec.AuthScheme == nil {
				return ""
			}
			return string(policy.Spec.AuthScheme.Authentication["kubernetes-user"].Overrides["fairness"].Expression)
		}).ShouldNot(ContainSubstring("system:serviceaccount:grid-test-identity:grid-client-second"))
	}, Entry("inline parentRefs", true), Entry("referenced HTTPRoute parentRefs", false))

	It("prefers HTTPS when both internal protocols are reported", func(ctx SpecContext) {
		httpsEndpoint := strings.Replace(endpoint, "http://", "https://", 1)
		httpsURL, err := apis.ParseURL(httpsEndpoint)
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() error {
			if err := envTest.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
				return err
			}
			address := svc.Status.Addresses[0]
			address.URL = httpsURL
			svc.Status.Addresses = append(svc.Status.Addresses, address)
			return envTest.Status().Update(ctx, svc)
		}).Should(Succeed())
		optIn(ctx)
		Expect(waitProvider(ctx).Object["spec"]).To(HaveKeyWithValue("endpoint", httpsEndpoint))
	})

	It("publishes the internal URL even when the preferred status URL is public", func(ctx SpecContext) {
		publicURL, err := apis.ParseURL(fmt.Sprintf("http://grid.example.com/%s/%s", ns.Name, svc.Name))
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() error {
			if err := envTest.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
				return err
			}
			publicAddress := svc.Status.Addresses[0]
			publicAddress.URL = publicURL
			publicAddress.Name = ptr.To("gateway-external")
			svc.Status.URL = publicURL
			svc.Status.Addresses = append([]kservev1alpha2.SourcedAddress{publicAddress}, svc.Status.Addresses...)
			return envTest.Status().Update(ctx, svc)
		}).Should(Succeed())
		optIn(ctx)
		Expect(waitProvider(ctx).Object["spec"]).To(HaveKeyWithValue("endpoint", endpoint))
	})

	DescribeTable("revokes providers for ineligible reported addresses", func(ctx SpecContext, addressName string, public bool) {
		optIn(ctx)
		provider := waitProvider(ctx)
		host := internalHost
		if public {
			host = "grid.example.com"
		}
		url, err := apis.ParseURL(fmt.Sprintf("http://%s/%s/%s", host, ns.Name, svc.Name))
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() error {
			if err := envTest.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
				return err
			}
			address := svc.Status.Addresses[0]
			address.Name = ptr.To(addressName)
			address.URL = url
			svc.Status.Addresses = []kservev1alpha2.SourcedAddress{address}
			return envTest.Status().Update(ctx, svc)
		}).Should(Succeed())
		Eventually(func() []unstructured.Unstructured { return providers(ctx) }).Should(BeEmpty())
		Consistently(func() []unstructured.Unstructured { return providers(ctx) }, "500ms").Should(BeEmpty())
		Expect(apierrors.IsNotFound(envTest.Get(ctx, client.ObjectKey{Namespace: ns.Name, Name: provider.GetName()}, &rbacv1.Role{}))).To(BeTrue())
		Expect(apierrors.IsNotFound(envTest.Get(ctx, client.ObjectKey{Namespace: ns.Name, Name: provider.GetName()}, &rbacv1.RoleBinding{}))).To(BeTrue())
	}, Entry("public URL", "gateway-internal", true),
		Entry("external address name", "gateway-external", false),
		Entry("missing address name", "", false),
		Entry("unsupported address name", "internal", false))

	It("updates model names and preserves administrative Grid settings", func(ctx SpecContext) {
		optIn(ctx)
		provider := waitProvider(ctx)
		Expect(unstructured.SetNestedField(provider.Object, true, "spec", "trafficPolicy", "drain")).To(Succeed())
		Expect(unstructured.SetNestedField(provider.Object, "grid-provider-gateway", "spec", "gatewayRef")).To(Succeed())
		Expect(unstructured.SetNestedField(provider.Object, "model-upstream", "spec", "routingClusterRef")).To(Succeed())
		Expect(envTest.Update(ctx, provider)).To(Succeed())
		updateService(ctx, func(s *kservev1alpha2.LLMInferenceService) { s.Spec.Model.Name = ptr.To("new/model") })
		Eventually(func() string {
			p := providers(ctx)
			if len(p) != 1 {
				return ""
			}
			models, _, err := unstructured.NestedSlice(p[0].Object, "spec", "models")
			Expect(err).NotTo(HaveOccurred())
			return models[0].(map[string]interface{})["name"].(string)
		}).Should(Equal("new/model"))
		provider = waitProvider(ctx)
		drain, _, err := unstructured.NestedBool(provider.Object, "spec", "trafficPolicy", "drain")
		Expect(err).NotTo(HaveOccurred())
		Expect(drain).To(BeTrue())
		Expect(provider.Object["spec"]).To(HaveKeyWithValue("gatewayRef", "grid-provider-gateway"))
		Expect(provider.Object["spec"]).To(HaveKeyWithValue("routingClusterRef", "model-upstream"))
	})

	It("inherits model names from configs without persisting the merged spec", func(ctx SpecContext) {
		cfg := &kservev1alpha2.LLMInferenceServiceConfig{ObjectMeta: metav1.ObjectMeta{Name: "grid-model-config", Namespace: ns.Name},
			Spec: kservev1alpha2.LLMInferenceServiceSpec{Model: kservev1alpha2.LLMModelSpec{Name: ptr.To("inherited/model")}},
		}
		Expect(envTest.Create(ctx, cfg)).To(Succeed())
		updateService(ctx, func(s *kservev1alpha2.LLMInferenceService) {
			s.Spec.Model.Name = nil
			s.Spec.BaseRefs = []corev1.LocalObjectReference{{Name: cfg.Name}}
		})
		optIn(ctx)
		provider := waitProvider(ctx)
		models, _, err := unstructured.NestedSlice(provider.Object, "spec", "models")
		Expect(err).NotTo(HaveOccurred())
		Expect(models[0]).To(HaveKeyWithValue("name", "inherited/model"))
		Expect(envTest.Get(ctx, client.ObjectKeyFromObject(svc), svc)).To(Succeed())
		Expect(svc.Spec.Model.Name).To(BeNil())
		Expect(envTest.Get(ctx, client.ObjectKeyFromObject(cfg), cfg)).To(Succeed())
		cfg.Spec.Model.Name = ptr.To("updated/inherited-model")
		Expect(envTest.Update(ctx, cfg)).To(Succeed())
		Eventually(func() interface{} {
			p := providers(ctx)
			if len(p) != 1 {
				return nil
			}
			models, _, err := unstructured.NestedSlice(p[0].Object, "spec", "models")
			Expect(err).NotTo(HaveOccurred())
			return models[0]
		}).Should(HaveKeyWithValue("name", "updated/inherited-model"))
	})

	DescribeTable("revokes the provider and authorization", func(ctx SpecContext, revoke string) {
		optIn(ctx)
		provider := waitProvider(ctx)
		switch revoke {
		case "annotation":
			optOut(ctx)
		case "namespace":
			Expect(envTest.Get(ctx, client.ObjectKeyFromObject(ns), ns)).To(Succeed())
			delete(ns.Labels, llmcontroller.GridAllowedLabel)
			Expect(envTest.Update(ctx, ns)).To(Succeed())
		case "gateway":
			Expect(client.IgnoreNotFound(envTest.Delete(ctx, gateway))).To(Succeed())
			// KServe, which is not running in envtest, withdraws the attachment.
			Eventually(func() error {
				if err := envTest.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
					return err
				}
				svc.Status.Router.Gateways = nil
				return envTest.Status().Update(ctx, svc)
			}).Should(Succeed())
		case "addresses":
			Eventually(func() error {
				if err := envTest.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
					return err
				}
				svc.Status.Addresses = nil
				return envTest.Status().Update(ctx, svc)
			}).Should(Succeed())
		case "deletion":
			Expect(envTest.Get(ctx, client.ObjectKeyFromObject(svc), svc)).To(Succeed())
			Expect(client.IgnoreNotFound(envTest.Delete(ctx, svc))).To(Succeed())
		}
		Eventually(func() []unstructured.Unstructured { return providers(ctx) }).Should(BeEmpty())
		for _, resource := range []client.Object{&rbacv1.Role{}, &rbacv1.RoleBinding{}} {
			Eventually(func() bool {
				return apierrors.IsNotFound(envTest.Get(ctx, client.ObjectKey{Namespace: ns.Name, Name: provider.GetName()}, resource))
			}).Should(BeTrue())
		}
	},
		Entry("when its Gateway configuration is removed", "annotation"),
		Entry("when its namespace no longer matches", "namespace"),
		Entry("when KServe withdraws the deleted Gateway attachment", "gateway"),
		Entry("when KServe withdraws its routing addresses", "addresses"),
		Entry("when the service is deleted", "deletion"),
	)

	DescribeTable("revokes access and schedules credential validation retries", func(ctx SpecContext, holdCleanup bool) {
		optIn(ctx)
		provider := waitProvider(ctx)
		if holdCleanup {
			provider.SetFinalizers([]string{"test.example.com/hold-credential-cleanup"})
			Expect(envTest.Update(ctx, provider)).To(Succeed())
			DeferCleanup(func(ctx SpecContext) {
				if err := envTest.Get(ctx, client.ObjectKeyFromObject(provider), provider); err == nil {
					provider.SetFinalizers(nil)
					Expect(envTest.Update(ctx, provider)).To(Succeed())
				}
			})
		}
		secret := &corev1.Secret{}
		key := client.ObjectKey{Namespace: "grid-test-identity", Name: "grid-token"}
		Expect(envTest.Get(ctx, key, secret)).To(Succeed())
		originalUID := secret.Annotations[corev1.ServiceAccountUIDKey]
		DeferCleanup(func(ctx SpecContext) {
			Expect(envTest.Get(ctx, key, secret)).To(Succeed())
			secret.Annotations[corev1.ServiceAccountUIDKey] = originalUID
			Expect(envTest.Update(ctx, secret)).To(Succeed())
		})
		secret.Annotations[corev1.ServiceAccountUIDKey] = "wrong-account-uid"
		Expect(envTest.Update(ctx, secret)).To(Succeed())
		// A service update triggers the same check as the minute refresh.
		updateService(ctx, func(s *kservev1alpha2.LLMInferenceService) { s.Annotations["test.example.com/recheck"] = "true" })
		reconciler := &llmcontroller.GridInferenceProviderReconciler{Client: envTest.Client, APIReader: envTest.Client}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(svc)}
		if holdCleanup {
			Eventually(func(g Gomega) {
				result, err := reconciler.Reconcile(ctx, request)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(result.RequeueAfter).To(Equal(5 * time.Second))
			}).Should(Succeed())
			Expect(envTest.Get(ctx, client.ObjectKeyFromObject(provider), provider)).To(Succeed())
			Expect(provider.GetDeletionTimestamp().IsZero()).To(BeFalse())
			Expect(envTest.Get(ctx, client.ObjectKeyFromObject(svc), svc)).To(Succeed())
			Expect(svc.Finalizers).To(ContainElement("grid.praxis.fast/inference-provider"))
			provider.SetFinalizers(nil)
			Expect(envTest.Update(ctx, provider)).To(Succeed())
		}
		Eventually(func(g Gomega) {
			result, err := reconciler.Reconcile(ctx, request)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(result.RequeueAfter).To(Equal(time.Minute))
		}).Should(Succeed())
		Eventually(func() []unstructured.Unstructured { return providers(ctx) }).Should(BeEmpty())
		Eventually(func() bool {
			return apierrors.IsNotFound(envTest.Get(ctx, client.ObjectKey{Namespace: ns.Name, Name: provider.GetName()}, &rbacv1.RoleBinding{}))
		}).Should(BeTrue())
	}, Entry("after cleanup completes", false), Entry("while cleanup waits for a provider finalizer", true))

	It("holds cleanup until Grid's provider finalizers complete and revokes access immediately", func(ctx SpecContext) {
		optIn(ctx)
		provider := waitProvider(ctx)
		provider.SetFinalizers([]string{"test.example.com/hold"})
		Expect(envTest.Update(ctx, provider)).To(Succeed())
		optOut(ctx)
		Eventually(func() bool {
			return apierrors.IsNotFound(envTest.Get(ctx, client.ObjectKey{Namespace: ns.Name, Name: provider.GetName()}, &rbacv1.RoleBinding{}))
		}).Should(BeTrue())
		Eventually(func() bool {
			Expect(envTest.Get(ctx, client.ObjectKeyFromObject(provider), provider)).To(Succeed())
			return !provider.GetDeletionTimestamp().IsZero()
		}).Should(BeTrue())
		Expect(envTest.Get(ctx, client.ObjectKeyFromObject(svc), svc)).To(Succeed())
		Expect(svc.Finalizers).To(ContainElement("grid.praxis.fast/inference-provider"))
		provider.SetFinalizers(nil)
		Expect(envTest.Update(ctx, provider)).To(Succeed())
		Eventually(func() []unstructured.Unstructured { return providers(ctx) }).Should(BeEmpty())
		Eventually(func() []string {
			Expect(envTest.Get(ctx, client.ObjectKeyFromObject(svc), svc)).To(Succeed())
			return svc.Finalizers
		}).ShouldNot(ContainElement("grid.praxis.fast/inference-provider"))
	})

	It("prefers the participant address over a root requiring a qualified model", func(ctx SpecContext) {
		Expect(envTest.Get(ctx, client.ObjectKeyFromObject(route), route)).To(Succeed())
		route.Spec.Rules = append(route.Spec.Rules, gatewayv1.HTTPRouteRule{Matches: []gatewayv1.HTTPRouteMatch{{
			Path:    &gatewayv1.HTTPPathMatch{Type: ptr.To(gatewayv1.PathMatchPathPrefix), Value: ptr.To("/")},
			Headers: []gatewayv1.HTTPHeaderMatch{{Name: "X-Gateway-Model-Name", Value: "publishers/" + ns.Name + "/models/test/model"}},
		}}})
		Expect(envTest.Update(ctx, route)).To(Succeed())
		for i := range route.Status.Parents[0].Conditions {
			route.Status.Parents[0].Conditions[i].ObservedGeneration = route.Generation
		}
		Expect(envTest.Status().Update(ctx, route)).To(Succeed())
		root, err := apis.ParseURL("http://" + internalHost + "/")
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() error {
			if err := envTest.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
				return err
			}
			participant := svc.Status.Addresses[len(svc.Status.Addresses)-1]
			participant.Models = []kservev1alpha2.ModelSourcedAddressStatus{{Name: "test/model"}}
			svc.Status.Addresses = []kservev1alpha2.SourcedAddress{
				{Addressable: duckv1.Addressable{Name: ptr.To("gateway-internal-model-routing"), URL: root}, Origin: participant.Origin, Models: []kservev1alpha2.ModelSourcedAddressStatus{{Name: "publishers/" + ns.Name + "/models/test/model"}}},
				participant,
			}
			return envTest.Status().Update(ctx, svc)
		}).Should(Succeed())
		optIn(ctx)
		provider := waitProvider(ctx)
		Expect(provider.Object["spec"]).To(HaveKeyWithValue("endpoint", endpoint))
		models, _, err := unstructured.NestedSlice(provider.Object, "spec", "models")
		Expect(err).NotTo(HaveOccurred())
		Expect(models[0]).To(HaveKeyWithValue("name", "test/model"))
	})

	It("uses the reported qualified model identifier for root-only routing", func(ctx SpecContext) {
		Expect(envTest.Get(ctx, client.ObjectKeyFromObject(route), route)).To(Succeed())
		route.Spec.Rules[0].Matches[0].Path.Value = ptr.To("/")
		route.Spec.Rules[0].Matches[0].Headers = []gatewayv1.HTTPHeaderMatch{{Name: "X-Gateway-Model-Name", Value: "publishers/" + ns.Name + "/models/test/model"}}
		Expect(envTest.Update(ctx, route)).To(Succeed())
		for i := range route.Status.Parents[0].Conditions {
			route.Status.Parents[0].Conditions[i].ObservedGeneration = route.Generation
		}
		Expect(envTest.Status().Update(ctx, route)).To(Succeed())
		root, err := apis.ParseURL("http://" + internalHost + "/")
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() error {
			if err := envTest.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
				return err
			}
			svc.Status.Addresses[0].URL = root
			svc.Status.Addresses[0].Name = ptr.To("gateway-internal-model-routing")
			svc.Status.Addresses[0].Models = []kservev1alpha2.ModelSourcedAddressStatus{{Name: "publishers/" + ns.Name + "/models/test/model"}}
			return envTest.Status().Update(ctx, svc)
		}).Should(Succeed())
		optIn(ctx)
		provider := waitProvider(ctx)
		Expect(provider.Object["spec"]).To(HaveKeyWithValue("endpoint", root.String()))
		models, _, err := unstructured.NestedSlice(provider.Object, "spec", "models")
		Expect(err).NotTo(HaveOccurred())
		Expect(models[0]).To(HaveKeyWithValue("name", "publishers/"+ns.Name+"/models/test/model"))
	})

	It("uses reported routing addresses without reading Services or HTTPRoutes", func(ctx SpecContext) {
		Expect(envTest.Delete(ctx, route)).To(Succeed())
		optIn(ctx)
		Expect(waitProvider(ctx).Object["spec"]).To(HaveKeyWithValue("endpoint", endpoint))
	})

	DescribeTable("prefers shared group addresses over participant URLs", func(ctx SpecContext, root bool) {
		groupURL := "http://" + internalHost + "/publishers/" + ns.Name + "/models/test/model"
		model := "test/model"
		if root {
			groupURL = "http://" + internalHost + "/"
			model = "publishers/" + ns.Name + "/models/test/model"
		}
		addressURL, err := apis.ParseURL(groupURL)
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() error {
			if err := envTest.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
				return err
			}
			svc.Status.Router.Group = &kservev1alpha2.GroupStatus{Name: "shared-model"}
			groupAddress := svc.Status.Addresses[0]
			groupAddress.URL = addressURL
			if root {
				groupAddress.Name = ptr.To("gateway-internal-model-routing")
			}
			groupAddress.Models = []kservev1alpha2.ModelSourcedAddressStatus{{Name: model}}
			svc.Status.Addresses = append(svc.Status.Addresses, groupAddress)
			return envTest.Status().Update(ctx, svc)
		}).Should(Succeed())
		optIn(ctx)
		provider := waitProvider(ctx)
		Expect(provider.Object["spec"]).To(HaveKeyWithValue("endpoint", groupURL))
		models, _, err := unstructured.NestedSlice(provider.Object, "spec", "models")
		Expect(err).NotTo(HaveOccurred())
		Expect(models[0]).To(HaveKeyWithValue("name", model))
	}, Entry("publisher path", false), Entry("root model-routing URL", true))

	It("keeps the source until held RBAC resources are actually deleted", func(ctx SpecContext) {
		optIn(ctx)
		provider := waitProvider(ctx)
		role := &rbacv1.Role{}
		binding := &rbacv1.RoleBinding{}
		key := client.ObjectKey{Namespace: ns.Name, Name: provider.GetName()}
		Expect(envTest.Get(ctx, key, role)).To(Succeed())
		Expect(envTest.Get(ctx, key, binding)).To(Succeed())
		for _, resource := range []client.Object{role, binding} {
			resource.SetFinalizers([]string{"test.example.com/hold-rbac"})
			Expect(envTest.Update(ctx, resource)).To(Succeed())
		}
		Expect(envTest.Get(ctx, client.ObjectKeyFromObject(svc), svc)).To(Succeed())
		Expect(envTest.Delete(ctx, svc)).To(Succeed())
		Eventually(func() []unstructured.Unstructured { return providers(ctx) }).Should(BeEmpty())
		Expect(envTest.Get(ctx, client.ObjectKeyFromObject(svc), svc)).To(Succeed())
		Expect(svc.Finalizers).To(ContainElement("grid.praxis.fast/inference-provider"))
		recreated := fixture.LLMInferenceService(svc.Name, fixture.WithModelURI("hf://test/model"))
		recreated.Namespace = ns.Name
		Expect(apierrors.IsAlreadyExists(envTest.Create(ctx, recreated))).To(BeTrue())
		Expect(envTest.Get(ctx, key, role)).To(Succeed())
		role.Finalizers = nil
		Expect(envTest.Update(ctx, role)).To(Succeed())
		Expect(envTest.Get(ctx, key, binding)).To(Succeed())
		Expect(binding.DeletionTimestamp.IsZero()).To(BeFalse())
		Expect(envTest.Get(ctx, client.ObjectKeyFromObject(svc), svc)).To(Succeed())
		Expect(svc.Finalizers).To(ContainElement("grid.praxis.fast/inference-provider"))
		binding.Finalizers = nil
		Expect(envTest.Update(ctx, binding)).To(Succeed())
		Eventually(func() bool {
			return apierrors.IsNotFound(envTest.Get(ctx, client.ObjectKeyFromObject(svc), &kservev1alpha2.LLMInferenceService{}))
		}).Should(BeTrue())
	})

	It("never publishes a service from an excluded namespace", func(ctx SpecContext) {
		Expect(envTest.Get(ctx, client.ObjectKeyFromObject(gateway), gateway)).To(Succeed())
		gateway.Annotations = map[string]string{llmcontroller.GridServingAnnotation: `{"networkName":"grid-test","namespace":"grid-test-identity","serviceAccount":"grid-client","tokenSecret":"grid-token"}`}
		Expect(envTest.Update(ctx, gateway)).To(Succeed())
		Consistently(func() []unstructured.Unstructured { return providers(ctx) }, "1s").Should(BeEmpty())
	})
})
