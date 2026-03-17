/*
Copyright 2025.

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

package fixture

import (
	"context"
	"fmt"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"

	kuadrantv1 "github.com/kuadrant/kuadrant-operator/api/v1"
	"github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func getHTTPRouteAuthPolicy(ctx context.Context, c client.Client, llmisvcNamespace, llmisvcName string) (*kuadrantv1.AuthPolicy, error) {
	httpRouteName := constants.GetHTTPRouteName(llmisvcName)
	return GetResourceByName(ctx, c, llmisvcNamespace, constants.GetHTTPRouteAuthPolicyName(httpRouteName), &kuadrantv1.AuthPolicy{})
}

func VerifyGatewayAuthPolicyOwnerRef(ctx context.Context, c client.Client, gatewayNamespace, gatewayName string) {
	gomega.Eventually(func() error {
		gatewayAuthPolicy, err := GetResourceByName(ctx, c, gatewayNamespace, constants.GetGatewayAuthPolicyName(gatewayName), &kuadrantv1.AuthPolicy{})
		if err != nil {
			return err
		}
		ownerRefs := gatewayAuthPolicy.GetOwnerReferences()
		gomega.Expect(ownerRefs).To(gomega.HaveLen(1))
		gomega.Expect(ownerRefs[0].Name).To(gomega.Equal(gatewayName))
		gomega.Expect(ownerRefs[0].Kind).To(gomega.Equal("Gateway"))
		gomega.Expect(ownerRefs[0].APIVersion).To(gomega.Equal("gateway.networking.k8s.io/v1"))
		return nil
	}).WithContext(ctx).Should(gomega.Succeed())
}

func VerifyHTTPRouteAuthPolicyOwnerRef(ctx context.Context, c client.Client, testNs string, llmisvcName string) {
	gomega.Eventually(func() error {
		httpRouteName := constants.GetHTTPRouteName(llmisvcName)
		httpRouteAuthPolicy, err := GetResourceByName(ctx, c, testNs, constants.GetHTTPRouteAuthPolicyName(httpRouteName), &kuadrantv1.AuthPolicy{})
		if err != nil {
			return err
		}
		ownerRefs := httpRouteAuthPolicy.GetOwnerReferences()
		gomega.Expect(ownerRefs).To(gomega.HaveLen(1))
		gomega.Expect(ownerRefs[0].Name).To(gomega.Equal(llmisvcName))
		gomega.Expect(ownerRefs[0].Kind).To(gomega.Equal("LLMInferenceService"))
		gomega.Expect(ownerRefs[0].APIVersion).To(gomega.Equal("serving.kserve.io/v1alpha1"))
		return nil
	}).WithContext(ctx).Should(gomega.Succeed())
}

func VerifyHTTPRouteAuthPolicyExists(ctx context.Context, c client.Client, testNs string, llmisvcName string) {
	httpRouteName := constants.GetHTTPRouteName(llmisvcName)
	VerifyResourceExists(ctx, c, testNs, constants.GetHTTPRouteAuthPolicyName(httpRouteName), &kuadrantv1.AuthPolicy{})
}

func VerifyCustomHTTPRouteAuthPolicyExists(ctx context.Context, c client.Client, testNs string, llmisvcName string, httpRouteName string) {
	gomega.Eventually(func() error {
		authPolicy, err := GetResourceByName(ctx, c, testNs, constants.GetHTTPRouteAuthPolicyName(httpRouteName), &kuadrantv1.AuthPolicy{})
		if err != nil {
			return err
		}
		// Verify the AuthPolicy targets the correct HTTPRoute
		if string(authPolicy.Spec.TargetRef.Name) != httpRouteName {
			return fmt.Errorf("expected AuthPolicy to target HTTPRoute %s, but got %s", httpRouteName, authPolicy.Spec.TargetRef.Name)
		}
		return nil
	}).WithContext(ctx).Should(gomega.Succeed())
}

func VerifyHTTPRouteAuthPolicyNotExist(ctx context.Context, c client.Client, testNs string, llmisvcName string) {
	httpRouteName := constants.GetHTTPRouteName(llmisvcName)
	VerifyResourceNotExist(ctx, c, testNs, constants.GetHTTPRouteAuthPolicyName(httpRouteName), &kuadrantv1.AuthPolicy{})
}

func VerifyGatewayAuthPolicyRestored(ctx context.Context, c client.Client, gatewayNamespace, gatewayName string, expectedTargetRefName gatewayapiv1.ObjectName) {
	gomega.Eventually(func() bool {
		restored, err := GetResourceByName(ctx, c, gatewayNamespace, constants.GetGatewayAuthPolicyName(gatewayName), &kuadrantv1.AuthPolicy{})
		if err != nil {
			return false
		}
		return restored.Spec.TargetRef.Name == expectedTargetRefName
	}).WithContext(ctx).Should(gomega.BeTrue())
}

func VerifyHTTPRouteAuthPolicyRestored(ctx context.Context, c client.Client, testNs string, llmisvcName string, expectedTargetRefName gatewayapiv1.ObjectName) {
	gomega.Eventually(func() bool {
		restored, err := getHTTPRouteAuthPolicy(ctx, c, testNs, llmisvcName)
		if err != nil {
			return false
		}
		return restored.Spec.TargetRef.Name == expectedTargetRefName
	}).WithContext(ctx).Should(gomega.BeTrue())
}

func WaitForCustomHTTPRouteAuthPolicy(ctx context.Context, c client.Client, testNs string, httpRouteName string) *kuadrantv1.AuthPolicy {
	var httpRouteAuthPolicy *kuadrantv1.AuthPolicy
	gomega.Eventually(func() error {
		var err error
		httpRouteAuthPolicy, err = GetResourceByName(ctx, c, testNs, constants.GetHTTPRouteAuthPolicyName(httpRouteName), &kuadrantv1.AuthPolicy{})
		return err
	}).WithContext(ctx).Should(gomega.Succeed())
	return httpRouteAuthPolicy
}

func VerifyCustomHTTPRouteAuthPolicyRestored(ctx context.Context, c client.Client, testNs string, httpRouteName string, expectedTargetRefName gatewayapiv1.ObjectName) {
	gomega.Eventually(func() bool {
		restored, err := GetResourceByName(ctx, c, testNs, constants.GetHTTPRouteAuthPolicyName(httpRouteName), &kuadrantv1.AuthPolicy{})
		if err != nil {
			return false
		}
		return restored.Spec.TargetRef.Name == expectedTargetRefName
	}).WithContext(ctx).Should(gomega.BeTrue())
}
