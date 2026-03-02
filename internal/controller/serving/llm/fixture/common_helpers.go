package fixture

import (
	"context"
	"fmt"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func CreateBasicLLMInferenceService(ctx context.Context, c client.Client, testNs string, llmisvcName string) *kservev1alpha1.LLMInferenceService {
	opts := []LLMInferenceServiceOption{
		InNamespace[*kservev1alpha1.LLMInferenceService](testNs),
	}

	llmisvc := LLMInferenceService(llmisvcName, opts...)
	gomega.Expect(c.Create(ctx, llmisvc)).Should(gomega.Succeed())
	return llmisvc
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
