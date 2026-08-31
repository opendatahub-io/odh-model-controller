package llm_test

import (
	"context"

	kedaapi "github.com/kedacore/keda/v2/apis/keda/v1alpha1"
	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/serving/llm/fixture"
	parentreconcilers "github.com/opendatahub-io/odh-model-controller/internal/controller/serving/reconcilers"
	pkgtest "github.com/opendatahub-io/odh-model-controller/internal/controller/testing"
	testutils "github.com/opendatahub-io/odh-model-controller/test/utils"
)

// These specs exercise LLMKEDAReconciler through the *live* controller-runtime reconcile loop (via
// envTest's manager), rather than invoking the reconciler directly. That's the only way to prove the
// Owns() watches registered in LLMInferenceServiceReconciler.SetupWithManager for the shared KEDA
// Prometheus auth resources (ServiceAccount, Role, TriggerAuthentication, ...) actually re-trigger
// reconciliation when those resources drift or are deleted out-of-band.
var _ = Describe("LLMInferenceService KEDA Prometheus auth resource watches", func() {
	var testNs string

	BeforeEach(func(ctx SpecContext) {
		testNs = testutils.Namespaces.Create(ctx, envTest.Client).Name
	})

	AfterEach(func(ctx SpecContext) {
		llmList := &kservev1alpha2.LLMInferenceServiceList{}
		if err := envTest.Client.List(ctx, llmList, client.InNamespace(testNs)); err == nil {
			for i := range llmList.Items {
				_ = envTest.Client.Delete(ctx, &llmList.Items[i])
			}
		}
	})

	prometheusScaling := func() *kservev1alpha2.ScalingSpec {
		return &kservev1alpha2.ScalingSpec{
			MaxReplicas: 3,
			KEDA: &kservev1alpha2.DirectKEDAScalingSpec{
				Triggers: []kedaapi.ScaleTriggers{
					{
						Type: "prometheus",
						Metadata: map[string]string{
							"serverAddress": "https://thanos-querier.openshift-monitoring.svc.cluster.local:9092",
							"query":         "sum(rate(vllm_request_success_total[1m]))",
							"threshold":     "10",
						},
					},
				},
			},
		}
	}

	createLLMISVCWithKEDA := func(ctx context.Context) *kservev1alpha2.LLMInferenceService {
		llmisvc := fixture.LLMInferenceService(pkgtest.GenerateUniqueTestName("keda-llmisvc"),
			fixture.InNamespace[*kservev1alpha2.LLMInferenceService](testNs),
			fixture.WithScaling(prometheusScaling()),
		)
		Expect(envTest.Client.Create(ctx, llmisvc)).Should(Succeed())
		return llmisvc
	}

	It("creates the shared KEDA auth resources through the live reconcile loop", func(ctx SpecContext) {
		createLLMISVCWithKEDA(ctx)

		Eventually(func() error {
			return envTest.Client.Get(ctx, types.NamespacedName{Namespace: testNs, Name: parentreconcilers.KEDAPrometheusAuthServiceAccountName}, &corev1.ServiceAccount{})
		}).WithContext(ctx).Should(Succeed())

		Eventually(func() error {
			return envTest.Client.Get(ctx, types.NamespacedName{Namespace: testNs, Name: parentreconcilers.KEDAPrometheusAuthTriggerAuthName}, &kedaapi.TriggerAuthentication{})
		}).WithContext(ctx).Should(Succeed())
	})

	It("recreates the ServiceAccount when it is deleted out-of-band", func(ctx SpecContext) {
		createLLMISVCWithKEDA(ctx)

		sa := &corev1.ServiceAccount{}
		Eventually(func() error {
			return envTest.Client.Get(ctx, types.NamespacedName{Namespace: testNs, Name: parentreconcilers.KEDAPrometheusAuthServiceAccountName}, sa)
		}).WithContext(ctx).Should(Succeed())

		Expect(envTest.Client.Delete(ctx, sa)).Should(Succeed())

		Eventually(func() error {
			return envTest.Client.Get(ctx, types.NamespacedName{Namespace: testNs, Name: parentreconcilers.KEDAPrometheusAuthServiceAccountName}, &corev1.ServiceAccount{})
		}).WithContext(ctx).Should(Succeed())
	})

	It("recreates the TriggerAuthentication when it is deleted out-of-band", func(ctx SpecContext) {
		createLLMISVCWithKEDA(ctx)

		ta := &kedaapi.TriggerAuthentication{}
		Eventually(func() error {
			return envTest.Client.Get(ctx, types.NamespacedName{Namespace: testNs, Name: parentreconcilers.KEDAPrometheusAuthTriggerAuthName}, ta)
		}).WithContext(ctx).Should(Succeed())

		Expect(envTest.Client.Delete(ctx, ta)).Should(Succeed())

		Eventually(func() error {
			return envTest.Client.Get(ctx, types.NamespacedName{Namespace: testNs, Name: parentreconcilers.KEDAPrometheusAuthTriggerAuthName}, &kedaapi.TriggerAuthentication{})
		}).WithContext(ctx).Should(Succeed())
	})

	It("restores the Role's rules when they are modified out-of-band", func(ctx SpecContext) {
		createLLMISVCWithKEDA(ctx)

		role := &rbacv1.Role{}
		Eventually(func() error {
			return envTest.Client.Get(ctx, types.NamespacedName{Namespace: testNs, Name: parentreconcilers.KEDAPrometheusAuthMetricsReaderRoleName}, role)
		}).WithContext(ctx).Should(Succeed())

		role.Rules = nil
		Expect(envTest.Client.Update(ctx, role)).Should(Succeed())

		Eventually(func() []rbacv1.PolicyRule {
			current := &rbacv1.Role{}
			if err := envTest.Client.Get(ctx, types.NamespacedName{Namespace: testNs, Name: parentreconcilers.KEDAPrometheusAuthMetricsReaderRoleName}, current); err != nil {
				return nil
			}
			return current.Rules
		}).WithContext(ctx).ShouldNot(BeEmpty())
	})
})
