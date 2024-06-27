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
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
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
			testNamespace := Namespaces.Create(cli)
			testNs = testNamespace.Name

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
			// We need to stub the cluster state and indicate where is istio namespace (reusing authConfig test data)
			if dsciErr := createDSCI(DSCIWithoutAuthorization); dsciErr != nil && !errors.IsAlreadyExists(dsciErr) {
				Fail(dsciErr.Error())
			}
			// Create a new InferenceService
			inferenceService := &kservev1beta1.InferenceService{}
			err := convertToStructuredResource(KserveInferenceServicePath1, inferenceService)
			Expect(err).NotTo(HaveOccurred())
			inferenceService.SetNamespace(testNs)
			Expect(cli.Create(ctx, inferenceService)).Should(Succeed())
			// Update the URL of the InferenceService to indicate it is ready.
			deployedInferenceService := &kservev1beta1.InferenceService{}
			err = cli.Get(ctx, types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}, deployedInferenceService)
			Expect(err).NotTo(HaveOccurred())
			// url, err := apis.ParseURL("https://example-onnx-mnist-default.test.com")
			Expect(err).NotTo(HaveOccurred())
			newAddress := &duckv1.Addressable{
				URL: apis.HTTPS("example-onnx-mnist-default.test.com"),
			}
			deployedInferenceService.Status.Address = newAddress
			err = cli.Status().Update(ctx, deployedInferenceService)
			Expect(err).NotTo(HaveOccurred())
			// Stub: Create a Kserve Service, which must be created by the KServe operator.
			svc := &corev1.Service{}
			err = convertToStructuredResource(testIsvcSvcPath, svc)
			Expect(err).NotTo(HaveOccurred())
			svc.SetNamespace(inferenceService.Namespace)
			Expect(cli.Create(ctx, svc)).Should(Succeed())
			err = cli.Status().Update(ctx, deployedInferenceService)
			Expect(err).NotTo(HaveOccurred())
			// isvcService, err := waitForService(cli, testNs, inferenceService.Name, 5, 2*time.Second)
			// Expect(err).NotTo(HaveOccurred())

			isvcService := &corev1.Service{}
			Eventually(func() error {
				err := cli.Get(ctx, client.ObjectKey{Namespace: inferenceService.Namespace, Name: inferenceService.Name}, isvcService)
				if err != nil {
					return err
				}
				if isvcService.Annotations == nil || isvcService.Annotations[constants.ServingCertAnnotationKey] == "" {

					return fmt.Errorf("Annotation[constants.ServingCertAnnotationKey] is not added yet")
				}
				return nil
			}, timeout, interval).Should(Succeed())

			Expect(isvcService.Annotations[constants.ServingCertAnnotationKey]).Should(Equal(inferenceService.Name))
		})

		It("should create a secret for runtime and update kserve local gateway in the istio-system namespace", func() {
			// We need to stub the cluster state and indicate where is istio namespace (reusing authConfig test data)
			if dsciErr := createDSCI(DSCIWithoutAuthorization); dsciErr != nil && !errors.IsAlreadyExists(dsciErr) {
				Fail(dsciErr.Error())
			}
			// Stub: Create a kserve-local-gateway, which must be created by the OpenDataHub operator.
			kserveLocalGateway := &istioclientv1beta1.Gateway{}
			err := convertToStructuredResource(kserveLocalGatewayPath, kserveLocalGateway)
			Expect(err).NotTo(HaveOccurred())
			Expect(cli.Create(ctx, kserveLocalGateway)).Should(Succeed())

			// Stub: Create a certificate Secret, which must be created by the openshift service-ca operator.
			secret := &corev1.Secret{}
			err = convertToStructuredResource(testIsvcSvcSecretPath, secret)
			Expect(err).NotTo(HaveOccurred())
			secret.SetNamespace(testNs)
			Expect(cli.Create(ctx, secret)).Should(Succeed())

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

			newAddress := &duckv1.Addressable{
				URL: apis.HTTPS("example-onnx-mnist-default.test.com"),
			}
			deployedInferenceService.Status.Address = newAddress

			err = cli.Status().Update(ctx, deployedInferenceService)
			Expect(err).NotTo(HaveOccurred())

			// Verify that the certificate secret is created in the istio-system namespace.
			Eventually(func() error {
				secret := &corev1.Secret{}
				return cli.Get(ctx, client.ObjectKey{Namespace: constants.IstioNamespace, Name: fmt.Sprintf("%s-%s", inferenceService.Name, inferenceService.Namespace)}, secret)
			}, timeout, interval).Should(Succeed())

			// Verify that the gateway is updated in the istio-system namespace.
			var gateway *istioclientv1beta1.Gateway
			Eventually(func() error {
				gateway, err = waitForUpdatedGatewayCompletion(cli, "add", constants.IstioNamespace, constants.KServeGatewayName, inferenceService.Name)
				return err
			}, timeout, interval).Should(Succeed())

			// Ensure that the server is successfully added to the KServe local gateway within the istio-system namespace.
			targetServerExist := hasServerFromGateway(gateway, fmt.Sprintf("%s-%s", "https", inferenceService.Name))
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

	Context("when there is a existing inferenceService", func() {
		var testNs string
		var isvcName string

		BeforeEach(func() {
			ctx := context.Background()
			testNamespace := Namespaces.Create(cli)
			testNs = testNamespace.Name

			inferenceServiceConfig := &corev1.ConfigMap{}
			Expect(convertToStructuredResource(InferenceServiceConfigPath1, inferenceServiceConfig)).To(Succeed())
			if err := cli.Create(ctx, inferenceServiceConfig); err != nil && !errors.IsAlreadyExists(err) {
				Fail(err.Error())
			}

			// We need to stub the cluster state and indicate where is istio namespace (reusing authConfig test data)
			if dsciErr := createDSCI(DSCIWithoutAuthorization); dsciErr != nil && !errors.IsAlreadyExists(dsciErr) {
				Fail(dsciErr.Error())
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

			// Stub: Create a certificate Secret, which must be created by the openshift service-ca operator.
			secret := &corev1.Secret{}
			err = convertToStructuredResource(testIsvcSvcSecretPath, secret)
			Expect(err).NotTo(HaveOccurred())
			secret.SetNamespace(testNs)
			Expect(cli.Create(ctx, secret)).Should(Succeed())

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

			newAddress := &duckv1.Addressable{
				URL: apis.HTTPS("example-onnx-mnist-default.test.com"),
			}
			deployedInferenceService.Status.Address = newAddress

			err = cli.Status().Update(ctx, deployedInferenceService)
			Expect(err).NotTo(HaveOccurred())

			// Verify that the certificate secret is created in the istio-system namespace.
			Eventually(func() error {
				secret := &corev1.Secret{}
				return cli.Get(ctx, types.NamespacedName{Name: inferenceService.Name, Namespace: inferenceService.Namespace}, secret)
			}, timeout, interval).Should(Succeed())

			Eventually(func() error {
				return cli.Get(ctx, client.ObjectKey{Namespace: constants.IstioNamespace, Name: fmt.Sprintf("%s-%s", inferenceService.Name, inferenceService.Namespace)}, secret)
			}, timeout, interval).Should(Succeed())

			// Verify that the gateway is updated in the istio-system namespace.
			var gateway *istioclientv1beta1.Gateway
			Eventually(func() error {
				gateway, err = waitForUpdatedGatewayCompletion(cli, "add", constants.IstioNamespace, constants.KServeGatewayName, inferenceService.Name)
				return err
			}, timeout, interval).Should(Succeed())

			// Ensure that the server is successfully added to the KServe local gateway within the istio-system namespace.
			targetServerExist := hasServerFromGateway(gateway, fmt.Sprintf("%s-%s", "https", inferenceService.Name))
			Expect(targetServerExist).Should(BeTrue())
		})

		When("serving cert Secret is rotated", func() {
			It("should re-sync serving cert Secret to istio-system", func() {
				deployedInferenceService := &kservev1beta1.InferenceService{}
				err := cli.Get(ctx, types.NamespacedName{Name: isvcName, Namespace: testNs}, deployedInferenceService)
				Expect(err).NotTo(HaveOccurred())

				// Get source secret
				srcSecret := &corev1.Secret{}
				err = cli.Get(ctx, client.ObjectKey{Namespace: testNs, Name: deployedInferenceService.Name}, srcSecret)
				Expect(err).NotTo(HaveOccurred())

				// Update source secret
				updatedDataString := "updateData"
				srcSecret.Data["tls.crt"] = []byte(updatedDataString)
				srcSecret.Data["tls.key"] = []byte(updatedDataString)
				Expect(cli.Update(ctx, srcSecret)).Should(Succeed())

				// Get destination secret
				err = cli.Get(ctx, client.ObjectKey{Namespace: testNs, Name: deployedInferenceService.Name}, srcSecret)
				Expect(err).NotTo(HaveOccurred())

				// Verify that the certificate secret in the istio-system namespace is updated.
				destSecret := &corev1.Secret{}
				Eventually(func() error {
					Expect(cli.Get(ctx, client.ObjectKey{Namespace: constants.IstioNamespace, Name: fmt.Sprintf("%s-%s", deployedInferenceService.Name, deployedInferenceService.Namespace)}, destSecret)).Should(Succeed())
					if string(destSecret.Data["tls.crt"]) != updatedDataString {
						return fmt.Errorf("destSecret is not updated yet")
					}
					return nil
				}, timeout, interval).Should(Succeed())

				Expect(destSecret.Data).To(Equal(srcSecret.Data))
			})
		})

		When("infereceService is deleted", func() {
			It("should remove the Server from the kserve local gateway in istio-system and delete the created Secret", func() {
				// Delete the existing ISVC
				deployedInferenceService := &kservev1beta1.InferenceService{}
				err := cli.Get(ctx, types.NamespacedName{Name: isvcName, Namespace: testNs}, deployedInferenceService)
				Expect(err).NotTo(HaveOccurred())
				Expect(cli.Delete(ctx, deployedInferenceService)).Should(Succeed())

				// Verify that the gateway is updated in the istio-system namespace.
				var gateway *istioclientv1beta1.Gateway
				Eventually(func() error {
					gateway, err = waitForUpdatedGatewayCompletion(cli, "delete", constants.IstioNamespace, constants.KServeGatewayName, isvcName)
					return err
				}, timeout, interval).Should(Succeed())

				// Ensure that the server is successfully removed from the KServe local gateway within the istio-system namespace.
				targetServerExist := hasServerFromGateway(gateway, isvcName)
				Expect(targetServerExist).Should(BeFalse())

				// Ensure that the synced Secret is successfully deleted within the istio-system namespace.
				secret := &corev1.Secret{}
				Eventually(func() error {
					return cli.Get(ctx, client.ObjectKey{Namespace: constants.IstioNamespace, Name: fmt.Sprintf("%s-%s", isvcName, constants.IstioNamespace)}, secret)
				}, timeout, interval).ShouldNot(Succeed())
			})
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

func waitForUpdatedGatewayCompletion(cli client.Client, op string, namespace, gatewayName string, isvcName string) (*istioclientv1beta1.Gateway, error) {
	ctx := context.Background()
	portName := fmt.Sprintf("%s-%s", "https", isvcName)
	gateway := &istioclientv1beta1.Gateway{}

	// Get the Gateway resource
	err := cli.Get(ctx, client.ObjectKey{Namespace: namespace, Name: gatewayName}, gateway)
	if err != nil {
		return nil, fmt.Errorf("failed to get Gateway: %w", err)
	}

	// Check conditions based on operation (op)
	switch op {
	case "add":
		if !hasServerFromGateway(gateway, portName) {
			return nil, fmt.Errorf("server %s not found in Gateway %s", portName, gatewayName)
		}
	case "delete":
		if hasServerFromGateway(gateway, portName) {
			return nil, fmt.Errorf("server %s still exists in Gateway %s", portName, gatewayName)
		}
	default:
		return nil, fmt.Errorf("unsupported operation: %s", op)
	}

	return gateway, nil
}

// checks if the server exists for the given gateway
func hasServerFromGateway(gateway *istioclientv1beta1.Gateway, portName string) bool {
	targetServerExist := false
	for _, server := range gateway.Spec.Servers {
		if server.Port.Name == portName {
			targetServerExist = true
			break
		}
	}
	return targetServerExist
}
