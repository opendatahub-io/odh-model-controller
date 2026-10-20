//go:build e2e

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

package authorino_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	authorinov1beta3 "github.com/kuadrant/authorino/api/v1beta3"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"knative.dev/pkg/apis"
	maistrav1 "maistra.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientconfig "sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
)

const testLabel = "test.opendatahub.io/authconfig-groups"

var (
	kube             client.Client
	clientset        *kubernetes.Clientset
	servingRuntime   kservev1alpha1.ServingRuntime
	httpClient       *http.Client
	meshNamespace    = "istio-system"
	meshControlPlane = "basic"
)

func TestServerlessAuthorization(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Serverless group authorization e2e")
}

var _ = BeforeSuite(func(ctx SpecContext) {
	scheme := runtime.NewScheme()
	Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	Expect(authorinov1beta3.AddToScheme(scheme)).To(Succeed())
	Expect(kservev1alpha1.AddToScheme(scheme)).To(Succeed())
	Expect(kservev1beta1.AddToScheme(scheme)).To(Succeed())
	Expect(maistrav1.AddToScheme(scheme)).To(Succeed())
	config, err := clientconfig.GetConfig()
	Expect(err).NotTo(HaveOccurred())
	kube, err = client.New(config, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
	clientset, err = kubernetes.NewForConfig(config)
	Expect(err).NotTo(HaveOccurred())

	// Reuse the ServingRuntime installed by KServe's OpenShift CI bootstrap.
	Expect(kube.Get(ctx, client.ObjectKey{
		Namespace: "kserve-ci-e2e-test", Name: "kserve-sklearnserver",
	}, &servingRuntime)).To(Succeed(), "run the KServe Serverless CI bootstrap first")
	if name := os.Getenv("CONTROL_PLANE_NAME"); name != "" {
		meshControlPlane = name
	}
	if namespace := os.Getenv("MESH_NAMESPACE"); namespace != "" {
		meshNamespace = namespace
	}
	if expectedImage := os.Getenv("ODH_MODEL_CONTROLLER_IMAGE"); expectedImage != "" {
		deployment, err := clientset.AppsV1().Deployments("opendatahub").Get(ctx,
			"odh-model-controller", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(deployment.Spec.Template.Spec.Containers[0].Image).To(Equal(expectedImage),
			"the deployed controller must be the image built from this PR")
	}

	roots, err := x509.SystemCertPool()
	Expect(err).NotTo(HaveOccurred())
	var routerCA corev1.Secret
	Expect(kube.Get(ctx, client.ObjectKey{
		Namespace: "openshift-ingress-operator", Name: "router-ca",
	}, &routerCA)).To(Succeed())
	Expect(roots.AppendCertsFromPEM(routerCA.Data[corev1.TLSCertKey])).To(BeTrue())
	httpClient = &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12, RootCAs: roots,
		}},
	}
	DeferCleanup(httpClient.CloseIdleConnections)
}, NodeTimeout(time.Minute))

var _ = DescribeTable("Group-only access through a Serverless inference endpoint",
	func(ctx SpecContext, graph bool) {
		namespace := createNamespace(ctx)
		otherNamespace := createNamespace(ctx)
		member := &maistrav1.ServiceMeshMember{
			ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: namespace.Name},
			Spec: maistrav1.ServiceMeshMemberSpec{ControlPlaneRef: maistrav1.ServiceMeshControlPlaneRef{
				Name: meshControlPlane, Namespace: meshNamespace,
			}},
		}
		Expect(kube.Create(ctx, member)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(kube.Get(ctx, client.ObjectKeyFromObject(member), member)).To(Succeed())
			g.Expect(member.Status.ObservedGeneration).To(Equal(member.Generation))
			g.Expect(member.Status.GetCondition(maistrav1.ConditionTypeMemberReady).Status).
				To(Equal(corev1.ConditionTrue))
		}).WithContext(ctx).WithTimeout(3 * time.Minute).WithPolling(time.Second).Should(Succeed())
		runtime := servingRuntime.DeepCopy()
		runtime.ObjectMeta = metav1.ObjectMeta{
			Name: servingRuntime.Name, Namespace: namespace.Name, Annotations: servingRuntime.Annotations,
		}
		if runtime.Spec.Annotations == nil {
			runtime.Spec.Annotations = map[string]string{}
		}
		runtime.Spec.Annotations["sidecar.istio.io/inject"] = "true"
		Expect(kube.Create(ctx, runtime)).To(Succeed())

		By("deploying a model and waiting for the controllers to publish its endpoint")
		model := createModel(ctx, namespace.Name, !graph)
		var protectedResource client.Object = model
		resourcePlural, authConfigName, endpoint := "inferenceservices", model.Name, model.Status.URL.String()
		if graph {
			inferenceGraph := createGraph(ctx, namespace.Name, model.Name)
			protectedResource = inferenceGraph
			resourcePlural = "inferencegraphs"
			authConfigName = inferenceGraph.Name + "-ig"
			endpoint = inferenceGraph.Status.URL.String()
		}
		var ac authorinov1beta3.AuthConfig
		Eventually(func(g Gomega) {
			g.Expect(kube.Get(ctx, client.ObjectKey{Namespace: namespace.Name, Name: authConfigName}, &ac)).To(Succeed())
			g.Expect(ac.OwnerReferences).To(ContainElement(HaveField("UID", protectedResource.GetUID())))
			g.Expect(ac.Status.Ready()).To(BeTrue())
		}).WithContext(ctx).WithTimeout(2 * time.Minute).WithPolling(time.Second).Should(Succeed())

		By("requiring authentication and denying tokens without model access")
		audiences := ac.Spec.Authentication["kubernetes-user"].KubernetesTokenReview.Audiences
		token := createToken(ctx, namespace.Name, audiences)
		otherToken := createToken(ctx, otherNamespace.Name, audiences)
		endpoint = strings.TrimRight(endpoint, "/") + "/v1/models/" + model.Name + ":predict"
		expectPrediction(ctx, endpoint, "", http.StatusUnauthorized)
		expectPrediction(ctx, endpoint, token, http.StatusForbidden)

		By("keeping a group binding scoped to a different resource name denied")
		role := &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{Name: "model-reader", Namespace: namespace.Name},
			Rules: []rbacv1.PolicyRule{{
				APIGroups: []string{"serving.kserve.io"}, Resources: []string{resourcePlural},
				ResourceNames: []string{"another-model"}, Verbs: []string{"get"},
			}},
		}
		Expect(kube.Create(ctx, role)).To(Succeed())
		binding := &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: role.Name, Namespace: namespace.Name},
			RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: role.Name},
			Subjects: []rbacv1.Subject{{
				Kind: rbacv1.GroupKind, APIGroup: rbacv1.GroupName,
				Name: "system:serviceaccounts:" + namespace.Name,
			}},
		}
		Expect(kube.Create(ctx, binding)).To(Succeed())
		expectPrediction(ctx, endpoint, token, http.StatusForbidden)

		By("granting the group access to the model and checking its actual predictions")
		role.Rules[0].ResourceNames = []string{protectedResource.GetName()}
		Expect(kube.Update(ctx, role)).To(Succeed())
		expectPrediction(ctx, endpoint, token, http.StatusOK)
		expectPrediction(ctx, endpoint, otherToken, http.StatusForbidden)

		By("revoking the group binding while using the same token")
		Expect(kube.Delete(ctx, binding)).To(Succeed())
		expectPrediction(ctx, endpoint, token, http.StatusForbidden)
	},
	Entry("for InferenceService", false, SpecTimeout(12*time.Minute)),
	Entry("for InferenceGraph", true, SpecTimeout(12*time.Minute)),
)

func createNamespace(ctx context.Context) *corev1.Namespace {
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "authconfig-groups-e2e-"}}
	ExpectWithOffset(1, kube.Create(ctx, namespace)).To(Succeed())
	DeferCleanup(func(ctx SpecContext) {
		if CurrentSpecReport().Failed() {
			for _, objects := range []client.ObjectList{
				&corev1.PodList{}, &corev1.EventList{}, &authorinov1beta3.AuthConfigList{},
				&kservev1beta1.InferenceServiceList{}, &kservev1alpha1.InferenceGraphList{},
			} {
				if err := kube.List(ctx, objects, client.InNamespace(namespace.Name)); err == nil {
					data, err := json.MarshalIndent(objects, "", "  ")
					if err == nil {
						_, _ = fmt.Fprintf(GinkgoWriter, "%s diagnostics: %s\n", namespace.Name, data)
					}
				}
			}
		}
		Expect(kube.Delete(ctx, namespace)).To(Succeed())
		Eventually(func() error {
			return kube.Get(ctx, client.ObjectKeyFromObject(namespace), &corev1.Namespace{})
		}).WithContext(ctx).WithTimeout(2 * time.Minute).WithPolling(time.Second).
			Should(Satisfy(apierrors.IsNotFound))
	}, NodeTimeout(3*time.Minute))
	return namespace
}

func createModel(ctx context.Context, namespace string, auth bool) *kservev1beta1.InferenceService {
	model := &kservev1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Name: "model", Namespace: namespace, Labels: map[string]string{testLabel: "true"},
			Annotations: map[string]string{
				"serving.kserve.io/deploymentMode":               "Serverless",
				"serving.knative.openshift.io/enablePassthrough": "true",
				"sidecar.istio.io/inject":                        "true",
				constants.EnableAuthODHAnnotation:                fmt.Sprint(auth),
			},
		},
		Spec: kservev1beta1.InferenceServiceSpec{Predictor: kservev1beta1.PredictorSpec{
			ComponentExtensionSpec: kservev1beta1.ComponentExtensionSpec{MinReplicas: ptr.To[int32](1)},
			Model: &kservev1beta1.ModelSpec{
				ModelFormat: kservev1beta1.ModelFormat{Name: "sklearn"}, Runtime: ptr.To(servingRuntime.Name),
				PredictorExtensionSpec: kservev1beta1.PredictorExtensionSpec{
					StorageURI: ptr.To("gs://kfserving-examples/models/sklearn/1.0/model"),
					Container: corev1.Container{Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("256Mi"),
						},
						Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("512Mi")},
					}},
				},
			},
		}},
	}
	ExpectWithOffset(1, kube.Create(ctx, model)).To(Succeed())
	EventuallyWithOffset(1, func(g Gomega) {
		g.Expect(kube.Get(ctx, client.ObjectKeyFromObject(model), model)).To(Succeed())
		g.Expect(model.Status.IsReady()).To(BeTrue())
		g.Expect(model.Status.URL).NotTo(BeNil())
	}).WithContext(ctx).WithTimeout(5 * time.Minute).WithPolling(time.Second).Should(Succeed())
	return model
}

func createGraph(ctx context.Context, namespace, modelName string) *kservev1alpha1.InferenceGraph {
	graph := &kservev1alpha1.InferenceGraph{
		ObjectMeta: metav1.ObjectMeta{
			Name: "graph", Namespace: namespace, Labels: map[string]string{testLabel: "true"},
			Annotations: map[string]string{
				"serving.kserve.io/deploymentMode":               "Serverless",
				"serving.knative.openshift.io/enablePassthrough": "true", constants.EnableAuthODHAnnotation: "true",
				"sidecar.istio.io/inject": "true",
			},
		},
		Spec: kservev1alpha1.InferenceGraphSpec{
			MinReplicas: ptr.To[int32](1),
			Nodes: map[string]kservev1alpha1.InferenceRouter{"root": {
				RouterType: kservev1alpha1.Sequence,
				Steps: []kservev1alpha1.InferenceStep{{InferenceTarget: kservev1alpha1.InferenceTarget{
					ServiceName: modelName,
				}}},
			}},
		},
	}
	ExpectWithOffset(1, kube.Create(ctx, graph)).To(Succeed())
	EventuallyWithOffset(1, func(g Gomega) {
		g.Expect(kube.Get(ctx, client.ObjectKeyFromObject(graph), graph)).To(Succeed())
		g.Expect(graph.Status.GetCondition(apis.ConditionReady).IsTrue()).To(BeTrue())
		g.Expect(graph.Status.URL).NotTo(BeNil())
	}).WithContext(ctx).WithTimeout(5 * time.Minute).WithPolling(time.Second).Should(Succeed())
	return graph
}

func createToken(ctx context.Context, namespace string, audiences []string) string {
	account := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "model-client", Namespace: namespace}}
	ExpectWithOffset(1, kube.Create(ctx, account)).To(Succeed())
	token, err := clientset.CoreV1().ServiceAccounts(namespace).CreateToken(ctx, account.Name,
		&authenticationv1.TokenRequest{Spec: authenticationv1.TokenRequestSpec{Audiences: audiences}},
		metav1.CreateOptions{})
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return token.Status.Token
}

func expectPrediction(ctx context.Context, endpoint, token string, expected int) {
	EventuallyWithOffset(1, func(g Gomega) {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
			strings.NewReader(`{"instances":[[6.8,2.8,4.8,1.4],[6.0,3.4,4.5,1.6]]}`))
		g.Expect(err).NotTo(HaveOccurred())
		request.Header.Set("Content-Type", "application/json")
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := httpClient.Do(request)
		g.Expect(err).NotTo(HaveOccurred())
		defer func() { _ = response.Body.Close() }()
		g.Expect(response.StatusCode).To(Equal(expected))
		if expected == http.StatusOK {
			var result struct {
				Predictions []int `json:"predictions"`
			}
			g.Expect(json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result)).To(Succeed())
			g.Expect(result.Predictions).To(Equal([]int{1, 1}))
		}
	}).WithContext(ctx).WithTimeout(time.Minute).WithPolling(time.Second).Should(Succeed())
}
