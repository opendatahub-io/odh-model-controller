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
	"time"

	"strings"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	gomegatypes "github.com/onsi/gomega/types"
	"github.com/opendatahub-io/odh-model-controller/controllers/constants"
	routev1 "github.com/openshift/api/route/v1"
	istioclientv1beta1 "istio.io/client-go/pkg/apis/networking/v1beta1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/apis"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	testIsvcSvcPath        = "./testdata/servingcert-service/test-isvc-svc.yaml"
	kserveLocalGatewayPath = "./testdata/gateway/kserve-local-gateway.yaml"
	testIsvcSvcSecretPath  = "./testdata/gateway/test-isvc-svc-secret.yaml"
)

var _ = Describe("The Openshift Kserve model controller", func() {

	When("creating a Kserve ServiceRuntime & InferenceService", func() {
		var testNs string

		BeforeEach(func() {
			ctx := context.Background()
			testNs = Namespaces.Get()
			testNamespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testNs,
					Namespace: testNs,
				},
			}
			Expect(cli.Create(ctx, testNamespace)).Should(Succeed())

			inferenceServiceConfig := &corev1.ConfigMap{}
			Expect(convertToStructuredResource(InferenceServiceConfigPath1, inferenceServiceConfig)).To(Succeed())
			if err := cli.Create(ctx, inferenceServiceConfig); err != nil && !errors.IsAlreadyExists(err) {
				Fail(err.Error())
			}

			servingRuntime := &kservev1alpha1.ServingRuntime{}
			Expect(convertToStructuredResource(KserveServingRuntimePath1, servingRuntime)).To(Succeed())
			if err := cli.Create(ctx, servingRuntime); err != nil && !errors.IsAlreadyExists(err) {
				Fail(err.Error())
			}
		})

		It("With Kserve InferenceService a Route be created", func() {
			inferenceService := &kservev1beta1.InferenceService{}
			err := convertToStructuredResource(KserveInferenceServicePath1, inferenceService)
			Expect(err).NotTo(HaveOccurred())
			inferenceService.SetNamespace(testNs)

			Expect(cli.Create(ctx, inferenceService)).Should(Succeed())

			By("By checking that the controller has not created the Route")
			Consistently(func() error {
				route := &routev1.Route{}
				key := types.NamespacedName{Name: getKServeRouteName(inferenceService), Namespace: constants.IstioNamespace}
				err = cli.Get(ctx, key, route)
				return err
			}, timeout, interval).Should(HaveOccurred())

			deployedInferenceService := &kservev1beta1.InferenceService{}
			err = cli.Get(ctx, types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}, deployedInferenceService)
			Expect(err).NotTo(HaveOccurred())

			url, err := apis.ParseURL("https://example-onnx-mnist-default.test.com")
			Expect(err).NotTo(HaveOccurred())
			deployedInferenceService.Status.URL = url

			err = cli.Status().Update(ctx, deployedInferenceService)
			Expect(err).NotTo(HaveOccurred())

			By("By checking that the controller has created the Route")
			Eventually(func() error {
				route := &routev1.Route{}
				key := types.NamespacedName{Name: getKServeRouteName(inferenceService), Namespace: constants.IstioNamespace}
				err = cli.Get(ctx, key, route)
				return err
			}, timeout, interval).Should(Succeed())
		})

		It("With a new Kserve InferenceService, serving cert annotation should be added to the runtime Service object.", func() {
			// Create a new InferenceService
			inferenceService := &kservev1beta1.InferenceService{}
			err := convertToStructuredResource(KserveInferenceServicePath1, inferenceService)
			Expect(err).NotTo(HaveOccurred())
			inferenceService.SetNamespace(testNs)
			Expect(cli.Create(ctx, inferenceService)).Should(Succeed())

			// Stub: Create a Runtime Service, which must be created by the OpenDataHub operator.
			svc := &corev1.Service{}
			err = convertToStructuredResource(testIsvcSvcPath, svc)
			Expect(err).NotTo(HaveOccurred())
			svc.SetNamespace(inferenceService.Namespace)
			Expect(cli.Create(ctx, svc)).Should(Succeed())

			// Update the URL of the InferenceService to indicate it is ready.
			deployedInferenceService := &kservev1beta1.InferenceService{}
			err = cli.Get(ctx, types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}, deployedInferenceService)
			Expect(err).NotTo(HaveOccurred())

			url, err := apis.ParseURL("https://example-onnx-mnist-default.test.com")
			Expect(err).NotTo(HaveOccurred())
			deployedInferenceService.Status.URL = url

			err = cli.Status().Update(ctx, deployedInferenceService)
			Expect(err).NotTo(HaveOccurred())

			isvcService, err := waitForService(cli, testNs, inferenceService.Name, 5, 2*time.Second)
			Expect(err).NotTo(HaveOccurred())

			Expect(isvcService.Annotations[constants.ServingCertAnnotationKey]).Should(Equal(inferenceService.Name))
		})

		It("should create a secret for runtime and update kserve local gateway in the istio-system namespace", func() {
			// Stub: Create a kserve-local-gateway, which must be created by the OpenDataHub operator.
			kserveLocalGateway := &istioclientv1beta1.Gateway{}
			err := convertToStructuredResource(kserveLocalGatewayPath, kserveLocalGateway)
			Expect(err).NotTo(HaveOccurred())
			Expect(cli.Create(ctx, kserveLocalGateway)).Should(Succeed())

			// Create a new InferenceService
			inferenceService := &kservev1beta1.InferenceService{}
			err = convertToStructuredResource(KserveInferenceServicePath1, inferenceService)
			Expect(err).NotTo(HaveOccurred())
			inferenceService.SetNamespace(testNs)

			Expect(cli.Create(ctx, inferenceService)).Should(Succeed())

			// Update the URL of the InferenceService to indicate it is ready.
			deployedInferenceService := &kservev1beta1.InferenceService{}
			err = cli.Get(ctx, types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}, deployedInferenceService)
			Expect(err).NotTo(HaveOccurred())

			url, err := apis.ParseURL("https://example-onnx-mnist-default.test.com")
			Expect(err).NotTo(HaveOccurred())
			deployedInferenceService.Status.URL = url

			err = cli.Status().Update(ctx, deployedInferenceService)
			Expect(err).NotTo(HaveOccurred())

			// Stub: Create a certificate Secret, which must be created by the openshift service-ca operator.
			secret := &corev1.Secret{}
			err = convertToStructuredResource(testIsvcSvcSecretPath, secret)
			Expect(err).NotTo(HaveOccurred())
			secret.SetNamespace(inferenceService.Namespace)
			Expect(cli.Create(ctx, secret)).Should(Succeed())

			// Verify that the certificate secret is found in the same namespace where the InferenceService is created.
			_, err = waitForSecret(cli, inferenceService.Namespace, inferenceService.Name, 5, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())

			// Verify that the certificate secret is created in the istio-system namespace.
			_, err = waitForSecret(cli, constants.IstioNamespace, inferenceService.Name, 5, 5*time.Second)
			Expect(err).NotTo(HaveOccurred())

			gateway, err := waitForUpdatedGatewayCompletion(cli, "add", constants.IstioNamespace, constants.KServeGatewayName, inferenceService.Name, 5, 2*time.Second)
			Expect(err).NotTo(HaveOccurred())

			// Ensure that the server is successfully added to the KServe local gateway within the istio-system namespace.
			targetServerExist := isServerExistFromGateway(gateway, inferenceService.Name)
			Expect(targetServerExist).Should(BeTrue())
		})

		It("should create required network policies when KServe is used", func() {
			// given
			inferenceService := &kservev1beta1.InferenceService{}
			Expect(convertToStructuredResource(KserveInferenceServicePath1, inferenceService)).To(Succeed())
			inferenceService.SetNamespace(testNs)

			// when
			Expect(cli.Create(ctx, inferenceService)).Should(Succeed())

			// then
			By("ensuring that the controller has created required network policies")
			networkPolicies := &v1.NetworkPolicyList{}
			Eventually(func() []v1.NetworkPolicy {
				err := cli.List(ctx, networkPolicies, client.InNamespace(inferenceService.Namespace))
				if err != nil {
					Fail(err.Error())
				}
				return networkPolicies.Items
			}, timeout, interval).Should(
				ContainElements(
					withMatchingNestedField("ObjectMeta.Name", Equal("allow-from-openshift-monitoring-ns")),
					withMatchingNestedField("ObjectMeta.Name", Equal("allow-openshift-ingress")),
					withMatchingNestedField("ObjectMeta.Name", Equal("allow-from-opendatahub-ns")),
				),
			)
		})
	})

	Context("when deleting a Kserve InferenceService", func() {
		var testNs string
		var isvcName string

		BeforeEach(func() {
			ctx := context.Background()
			testNs = Namespaces.Get()
			testNamespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testNs,
					Namespace: testNs,
				},
			}
			Expect(cli.Create(ctx, testNamespace)).Should(Succeed())

			inferenceServiceConfig := &corev1.ConfigMap{}
			Expect(convertToStructuredResource(InferenceServiceConfigPath1, inferenceServiceConfig)).To(Succeed())
			if err := cli.Create(ctx, inferenceServiceConfig); err != nil && !errors.IsAlreadyExists(err) {
				Fail(err.Error())
			}

			servingRuntime := &kservev1alpha1.ServingRuntime{}
			Expect(convertToStructuredResource(KserveServingRuntimePath1, servingRuntime)).To(Succeed())
			if err := cli.Create(ctx, servingRuntime); err != nil && !errors.IsAlreadyExists(err) {
				Fail(err.Error())
			}

			// Stub: Create a kserve-local-gateway, which must be created by the OpenDataHub operator.
			kserveLocalGateway := &istioclientv1beta1.Gateway{}
			err := convertToStructuredResource(kserveLocalGatewayPath, kserveLocalGateway)
			Expect(err).NotTo(HaveOccurred())

			Expect(cli.Create(ctx, kserveLocalGateway)).Should(Succeed())

			// Create a new InferenceService
			inferenceService := &kservev1beta1.InferenceService{}
			err = convertToStructuredResource(KserveInferenceServicePath1, inferenceService)
			Expect(err).NotTo(HaveOccurred())
			inferenceService.SetNamespace(testNs)
			// Ensure the Delete method is called when the InferenceService (ISVC) is deleted.
			inferenceService.SetFinalizers([]string{"finalizer.inferenceservice"})

			Expect(cli.Create(ctx, inferenceService)).Should(Succeed())
			isvcName = inferenceService.Name

			// Update the URL of the InferenceService to indicate it is ready.
			deployedInferenceService := &kservev1beta1.InferenceService{}
			err = cli.Get(ctx, types.NamespacedName{Name: inferenceService.Name, Namespace: testNs}, deployedInferenceService)
			Expect(err).NotTo(HaveOccurred())

			url, err := apis.ParseURL("https://example-onnx-mnist-default.test.com")
			Expect(err).NotTo(HaveOccurred())
			deployedInferenceService.Status.URL = url

			err = cli.Status().Update(ctx, deployedInferenceService)
			Expect(err).NotTo(HaveOccurred())

			// Stub: Create a certificate Secret, which must be created by the openshift service-ca operator.
			secret := &corev1.Secret{}
			err = convertToStructuredResource(testIsvcSvcSecretPath, secret)
			Expect(err).NotTo(HaveOccurred())
			secret.SetNamespace(testNs)
			Expect(cli.Create(ctx, secret)).Should(Succeed())

			// Verify that the certificate secret is created in the istio-system namespace.
			_, err = waitForSecret(cli, constants.IstioNamespace, inferenceService.Name, 5, 2*time.Second)
			Expect(err).NotTo(HaveOccurred())

			gateway, err := waitForUpdatedGatewayCompletion(cli, "add", constants.IstioNamespace, constants.KServeGatewayName, inferenceService.Name, 5, 2*time.Second)
			Expect(err).NotTo(HaveOccurred())

			// Ensure that the server is successfully added to the KServe local gateway within the istio-system namespace.
			targetServerExist := isServerExistFromGateway(gateway, inferenceService.Name)
			Expect(targetServerExist).Should(BeTrue())
		})

		It("should remove the Server from the kserve local gateway in istio-system and delete the created Secret", func() {
			// Delete the existing ISVC
			deployedInferenceService := &kservev1beta1.InferenceService{}
			err := cli.Get(ctx, types.NamespacedName{Name: isvcName, Namespace: testNs}, deployedInferenceService)
			Expect(err).NotTo(HaveOccurred())
			Expect(cli.Delete(ctx, deployedInferenceService)).Should(Succeed())

			gateway, err := waitForUpdatedGatewayCompletion(cli, "delete", constants.IstioNamespace, constants.KServeGatewayName, isvcName, 5, 2*time.Second)
			Expect(err).NotTo(HaveOccurred())

			// Ensure that the server is successfully removed from the KServe local gateway within the istio-system namespace.
			targetServerExist := isServerExistFromGateway(gateway, isvcName)
			Expect(targetServerExist).Should(BeFalse())

			// Ensure that the created Secret is successfully deleted within the istio-system namespace.
			secret, err := waitForSecret(cli, constants.IstioNamespace, isvcName, 5, 2*time.Second)
			Expect(err).To(HaveOccurred())
			Expect(secret).Should(BeNil())
		})
	})

})

func withMatchingNestedField(path string, matcher gomegatypes.GomegaMatcher) gomegatypes.GomegaMatcher {
	if path == "" {
		Fail("cannot handle empty path")
	}

	fields := strings.Split(path, ".")

	// Reverse the path, so we start composing matchers from the leaf up
	for i, j := 0, len(fields)-1; i < j; i, j = i+1, j-1 {
		fields[i], fields[j] = fields[j], fields[i]
	}

	matchFields := MatchFields(IgnoreExtras,
		Fields{fields[0]: matcher},
	)

	for i := 1; i < len(fields); i++ {
		matchFields = MatchFields(IgnoreExtras, Fields{fields[i]: matchFields})
	}

	return matchFields
}

func getKServeRouteName(isvc *kservev1beta1.InferenceService) string {
	return isvc.Name + "-" + isvc.Namespace
}

func waitForService(cli client.Client, namespace, serviceName string, maxTries int, delay time.Duration) (*corev1.Service, error) {
	time.Sleep(delay)

	ctx := context.Background()
	service := &corev1.Service{}
	for try := 1; try <= maxTries; try++ {
		err := cli.Get(ctx, client.ObjectKey{Namespace: namespace, Name: serviceName}, service)
		if err == nil {
			if service.GetAnnotations() == nil {
				time.Sleep(1 * time.Second)
				continue
			}
			return service, nil
		}
		if !errors.IsNotFound(err) {
			continue
		}

		if try < maxTries {
			time.Sleep(1 * time.Second)
			return nil, err
		}
	}
	return service, nil
}

// This function waits for the updated gateway completion after a specified delay.
// During the wait, it checks the gateway repeatedly until the specified operation (add or delete) is completed or the maximum number of tries is reached.
// If the maximum number of tries is exceeded, it returns an error.
func waitForUpdatedGatewayCompletion(cli client.Client, op string, namespace, gatewayName string, isvcName string, maxTries int, delay time.Duration) (*istioclientv1beta1.Gateway, error) {
	time.Sleep(delay)

	ctx := context.Background()
	gateway := &istioclientv1beta1.Gateway{}
	for try := 1; try <= maxTries; try++ {
		err := cli.Get(ctx, client.ObjectKey{Namespace: namespace, Name: gatewayName}, gateway)
		if err == nil {
			if op == "add" && !isServerExistFromGateway(gateway, isvcName) {
				time.Sleep(1 * time.Second)
				continue
			} else if op == "delete" && isServerExistFromGateway(gateway, isvcName) {
				time.Sleep(1 * time.Second)
				continue
			}
			return gateway, nil
		}
		if !errors.IsNotFound(err) {
			continue
		}

		if try < maxTries {
			time.Sleep(1 * time.Second)
			return nil, err
		}
	}
	return gateway, nil
}

// checks if the server exists for the given gateway
func isServerExistFromGateway(gateway *istioclientv1beta1.Gateway, inferenceServiceName string) bool {
	targetServerExist := false
	for _, server := range gateway.Spec.Servers {
		if server.Port.Name == inferenceServiceName {
			targetServerExist = true
			break
		}
	}
	return targetServerExist
}
