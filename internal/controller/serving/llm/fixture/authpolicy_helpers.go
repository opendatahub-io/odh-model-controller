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

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kuadrantv1 "github.com/kuadrant/kuadrant-operator/api/v1"
	"github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
)

func getAuthPolicyByName(ctx context.Context, c client.Client, namespace, authPolicyName string) (*kuadrantv1.AuthPolicy, error) {
	authPolicy := &kuadrantv1.AuthPolicy{}

	err := c.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      authPolicyName,
	}, authPolicy)

	return authPolicy, err
}

func GetGatewayAuthPolicy(ctx context.Context, c client.Client, gatewayNamespace, gatewayName string) (*kuadrantv1.AuthPolicy, error) {
	return getAuthPolicyByName(ctx, c, gatewayNamespace, constants.GetGatewayAuthPolicyName(gatewayName))
}

func GetHTTPRouteAuthPolicy(ctx context.Context, c client.Client, llmisvcNamespace, llmisvcName string) (*kuadrantv1.AuthPolicy, error) {
	httpRouteName := constants.GetHTTPRouteName(llmisvcName)
	return getAuthPolicyByName(ctx, c, llmisvcNamespace, constants.GetHTTPRouteAuthPolicyName(httpRouteName))
}

func CreateBasicLLMInferenceService(ctx context.Context, c client.Client, testNs string, llmisvcName string, enableAuth *bool) *kservev1alpha1.LLMInferenceService {
	opts := []LLMInferenceServiceOption{
		InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
	}
	if enableAuth != nil {
		opts = append(opts, WithEnableAuth(*enableAuth))
	}

	llmisvc := LLMInferenceService(llmisvcName, opts...)
	gomega.Expect(c.Create(ctx, llmisvc)).Should(gomega.Succeed())
	return llmisvc
}

func CreateHTTPRouteForLLMService(ctx context.Context, c client.Client, testNs string, llmisvcName string) {
	httproute := HTTPRoute(constants.GetHTTPRouteName(llmisvcName),
		InNamespace[*gatewayapiv1.HTTPRoute](testNs),
		WithParentRef(GatewayRef(constants.DefaultGatewayName,
			RefInNamespace(constants.DefaultGatewayNamespace))),
	)
	gomega.Expect(c.Create(ctx, httproute)).Should(gomega.Succeed())

	gomega.Eventually(func() error {
		route := &gatewayapiv1.HTTPRoute{}
		return c.Get(ctx, client.ObjectKey{
			Name:      constants.GetHTTPRouteName(llmisvcName),
			Namespace: testNs,
		}, route)
	}).WithContext(ctx).Should(gomega.Succeed())
}

func VerifyGatewayAuthPolicyOwnerRef(ctx context.Context, c client.Client, gatewayNamespace, gatewayName string) {
	gomega.Eventually(func() error {
		gatewayAuthPolicy, err := GetGatewayAuthPolicy(ctx, c, gatewayNamespace, gatewayName)
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
		httpRouteAuthPolicy, err := getAuthPolicyByName(ctx, c, testNs, constants.GetHTTPRouteAuthPolicyName(httpRouteName))
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

func WaitForHTTPRouteAuthPolicy(ctx context.Context, c client.Client, testNs string, llmisvcName string) *kuadrantv1.AuthPolicy {
	var httpRouteAuthPolicy *kuadrantv1.AuthPolicy
	gomega.Eventually(func() error {
		var err error
		httpRouteAuthPolicy, err = GetHTTPRouteAuthPolicy(ctx, c, testNs, llmisvcName)
		return err
	}).WithContext(ctx).Should(gomega.Succeed())
	return httpRouteAuthPolicy
}

func WaitForGatewayAuthPolicy(ctx context.Context, c client.Client, gatewayNamespace, gatewayName string) *kuadrantv1.AuthPolicy {
	var gatewayAuthPolicy *kuadrantv1.AuthPolicy
	gomega.Eventually(func() error {
		var err error
		gatewayAuthPolicy, err = GetGatewayAuthPolicy(ctx, c, gatewayNamespace, gatewayName)
		return err
	}).WithContext(ctx).Should(gomega.Succeed())
	return gatewayAuthPolicy
}

func VerifyHTTPRouteAuthPolicyExists(ctx context.Context, c client.Client, testNs string, llmisvcName string) {
	gomega.Eventually(func() error {
		_, err := GetHTTPRouteAuthPolicy(ctx, c, testNs, llmisvcName)
		return err
	}).WithContext(ctx).Should(gomega.Succeed())
}

func VerifyCustomHTTPRouteAuthPolicyExists(ctx context.Context, c client.Client, testNs string, llmisvcName string, httpRouteName string) {
	gomega.Eventually(func() error {
		authPolicy, err := getAuthPolicyByName(ctx, c, testNs, constants.GetHTTPRouteAuthPolicyName(httpRouteName))
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
	gomega.Eventually(func() error {
		_, err := GetHTTPRouteAuthPolicy(ctx, c, testNs, llmisvcName)
		if err != nil {
			if errors.IsNotFound(err) {
				return nil
			}
			return err
		}
		return fmt.Errorf("HTTPRoute AuthPolicy still exists, expected it to be deleted")
	}).WithContext(ctx).Should(gomega.Succeed())
}

func VerifyHTTPRouteAuthPolicyRecreated(ctx context.Context, c client.Client, testNs string, llmisvcName string) {
	gomega.Eventually(func() error {
		_, err := GetHTTPRouteAuthPolicy(ctx, c, testNs, llmisvcName)
		return err
	}).WithContext(ctx).Should(gomega.Succeed())

	gomega.Consistently(func() error {
		_, err := GetHTTPRouteAuthPolicy(ctx, c, testNs, llmisvcName)
		return err
	}).WithContext(ctx).Should(gomega.Succeed())
}

func VerifyGatewayAuthPolicyRecreated(ctx context.Context, c client.Client, gatewayNamespace, gatewayName string) {
	gomega.Eventually(func() error {
		_, err := GetGatewayAuthPolicy(ctx, c, gatewayNamespace, gatewayName)
		return err
	}).WithContext(ctx).Should(gomega.Succeed())

	gomega.Consistently(func() error {
		_, err := GetGatewayAuthPolicy(ctx, c, gatewayNamespace, gatewayName)
		return err
	}).WithContext(ctx).Should(gomega.Succeed())
}

func VerifyGatewayAuthPolicyRestored(ctx context.Context, c client.Client, gatewayNamespace, gatewayName string, expectedTargetRefName gatewayapiv1.ObjectName) {
	gomega.Eventually(func() bool {
		restored, err := GetGatewayAuthPolicy(ctx, c, gatewayNamespace, gatewayName)
		if err != nil {
			return false
		}
		return restored.Spec.TargetRef.Name == expectedTargetRefName
	}).WithContext(ctx).Should(gomega.BeTrue())
}

func VerifyHTTPRouteAuthPolicyRestored(ctx context.Context, c client.Client, testNs string, llmisvcName string, expectedTargetRefName gatewayapiv1.ObjectName) {
	gomega.Eventually(func() bool {
		restored, err := GetHTTPRouteAuthPolicy(ctx, c, testNs, llmisvcName)
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
		httpRouteAuthPolicy, err = getAuthPolicyByName(ctx, c, testNs, constants.GetHTTPRouteAuthPolicyName(httpRouteName))
		return err
	}).WithContext(ctx).Should(gomega.Succeed())
	return httpRouteAuthPolicy
}

func VerifyCustomHTTPRouteAuthPolicyRestored(ctx context.Context, c client.Client, testNs string, httpRouteName string, expectedTargetRefName gatewayapiv1.ObjectName) {
	gomega.Eventually(func() bool {
		restored, err := getAuthPolicyByName(ctx, c, testNs, constants.GetHTTPRouteAuthPolicyName(httpRouteName))
		if err != nil {
			return false
		}
		return restored.Spec.TargetRef.Name == expectedTargetRefName
	}).WithContext(ctx).Should(gomega.BeTrue())
}
