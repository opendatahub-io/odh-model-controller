package controllers

import (
	"context"
	"fmt"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
)

const (
	KserveSA = "kserve-sa"
)

var _ = Describe("The KServe Dashboard reconciler", func() {
	var testNs string

	createServingRuntime := func(namespace, path string) *kservev1alpha1.ServingRuntime {
		servingRuntime := &kservev1alpha1.ServingRuntime{}
		err := convertToStructuredResource(path, servingRuntime)
		Expect(err).NotTo(HaveOccurred())
		servingRuntime.SetNamespace(namespace)
		if err := cli.Create(ctx, servingRuntime); err != nil && !apierrs.IsAlreadyExists(err) {
			Fail(err.Error())
		}
		return servingRuntime
	}

	createInferenceService := func(namespace, name string, path string) *kservev1beta1.InferenceService {
		inferenceService := &kservev1beta1.InferenceService{}
		err := convertToStructuredResource(path, inferenceService)
		Expect(err).NotTo(HaveOccurred())
		inferenceService.SetNamespace(namespace)
		if len(name) != 0 {
			inferenceService.Name = name
		}
		inferenceService.Annotations = map[string]string{}
		inferenceService.Annotations["serving.kserve.io/deploymentMode"] = "RawDeployment"
		if err := cli.Create(ctx, inferenceService); err != nil && !apierrs.IsAlreadyExists(err) {
			Fail(err.Error())
		}
		return inferenceService
	}

	BeforeEach(func() {
		testNs = Namespaces.Create(cli).Name

		inferenceServiceConfig := &corev1.ConfigMap{}
		Expect(convertToStructuredResource(InferenceServiceConfigPath1, inferenceServiceConfig)).To(Succeed())
		if err := cli.Create(ctx, inferenceServiceConfig); err != nil && !apierrs.IsAlreadyExists(err) {
			Fail(err.Error())
		}

	})

	When("deploying a Kserve RawDeployment model", func() {
		It("it should create a service account and clusterrolebinding for auth", func() {
			_ = createServingRuntime(testNs, KserveServingRuntimePath1)
			_ = createInferenceService(testNs, KserveOvmsInferenceServiceName, KserveInferenceServicePath1)

			sa, err := waitForServiceAccount(cli, testNs, KserveSA, 30, 1*time.Second)
			Expect(err).NotTo(HaveOccurred())
			Expect(sa).NotTo(BeNil())

		})
	})
})

func waitForServiceAccount(cli client.Client, namespace, saName string, maxTries int, delay time.Duration) (*corev1.ServiceAccount, error) {
	time.Sleep(delay)

	ctx := context.Background()
	sa := &corev1.ServiceAccount{}
	for try := 1; try <= maxTries; try++ {
		err := cli.Get(ctx, client.ObjectKey{Namespace: namespace, Name: saName}, sa)
		if err == nil {
			return sa, nil
		}
		if apierrs.IsNotFound(err) {
			return nil, fmt.Errorf("failed to get configmap %s/%s: %v", namespace, saName, err)
		}

		if try > maxTries {
			time.Sleep(1 * time.Second)
			return nil, err
		}
	}
	return sa, nil
}
