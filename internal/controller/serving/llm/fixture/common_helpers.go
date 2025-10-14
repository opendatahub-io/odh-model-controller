package fixture

import (
	"context"
	"fmt"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/onsi/gomega"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

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

func GetResourceByName[T client.Object](ctx context.Context, c client.Client, namespace, name string, obj T) (T, error) {
	err := c.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}, obj)
	return obj, err
}

func WaitForResource[T client.Object](ctx context.Context, c client.Client, namespace, name string, obj T) T {
	gomega.Eventually(func() error {
		_, err := GetResourceByName(ctx, c, namespace, name, obj)
		return err
	}).WithContext(ctx).Should(gomega.Succeed())
	return obj
}

func CheckResourceExists[T client.Object](ctx context.Context, c client.Client, namespace, name string, obj T) error {
	_, err := GetResourceByName(ctx, c, namespace, name, obj)
	return err
}

func VerifyResourceExists[T client.Object](ctx context.Context, c client.Client, namespace, name string, obj T) {
	gomega.Eventually(func() error {
		return CheckResourceExists(ctx, c, namespace, name, obj)
	}).WithContext(ctx).Should(gomega.Succeed())

	gomega.Consistently(func() error {
		return CheckResourceExists(ctx, c, namespace, name, obj)
	}).WithContext(ctx).Should(gomega.Succeed())
}

func CheckResourceNotFound[T client.Object](ctx context.Context, c client.Client, namespace, name string, obj T) error {
	_, err := GetResourceByName(ctx, c, namespace, name, obj)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	return fmt.Errorf("resource still exists, expected it to be deleted")
}

func VerifyResourceNotExist[T client.Object](ctx context.Context, c client.Client, namespace, name string, obj T) {
	gomega.Eventually(func() error {
		return CheckResourceNotFound(ctx, c, namespace, name, obj)
	}).WithContext(ctx).Should(gomega.Succeed())

	gomega.Consistently(func() error {
		return CheckResourceNotFound(ctx, c, namespace, name, obj)
	}).WithContext(ctx).Should(gomega.Succeed())
}
