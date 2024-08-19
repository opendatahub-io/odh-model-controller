package controllers

import (
	"reflect"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	maistrav1 "maistra.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/controllers/constants"
	"github.com/opendatahub-io/odh-model-controller/controllers/utils"
)

var _ = Describe("The KServe mesh reconciler", func() {
	var testNs string

	createInferenceService := func(namespace, name string) *kservev1beta1.InferenceService {
		inferenceService := &kservev1beta1.InferenceService{}
		err := convertToStructuredResource(KserveInferenceServicePath1, inferenceService)
		Expect(err).NotTo(HaveOccurred())
		inferenceService.SetNamespace(namespace)
		if len(name) != 0 {
			inferenceService.Name = name
		}
		Expect(cli.Create(ctx, inferenceService)).Should(Succeed())

		return inferenceService
	}

	expectOwnedSmmCreated := func(namespace string) {
		Eventually(func() error {
			smm := &maistrav1.ServiceMeshMember{}
			key := types.NamespacedName{Name: constants.ServiceMeshMemberName, Namespace: namespace}
			err := cli.Get(ctx, key, smm)
			return err
		}, timeout, interval).Should(Succeed())
	}

	createUserOwnedMeshEnrolment := func(namespace string) *maistrav1.ServiceMeshMember {
		controlPlaneName, meshNamespace := utils.GetIstioControlPlaneName(ctx, cli)
		smm := &maistrav1.ServiceMeshMember{
			ObjectMeta: metav1.ObjectMeta{
				Name:        constants.ServiceMeshMemberName,
				Namespace:   namespace,
				Labels:      nil,
				Annotations: nil,
			},
			Spec: maistrav1.ServiceMeshMemberSpec{
				ControlPlaneRef: maistrav1.ServiceMeshControlPlaneRef{
					Name:      controlPlaneName,
					Namespace: meshNamespace,
				}},
			Status: maistrav1.ServiceMeshMemberStatus{},
		}
		Expect(cli.Create(ctx, smm)).Should(Succeed())

		return smm
	}

	BeforeEach(func() {
		testNamespace := Namespaces.Create(cli)
		testNs = testNamespace.Name

		inferenceServiceConfig := &corev1.ConfigMap{}
		Expect(convertToStructuredResource(InferenceServiceConfigPath1, inferenceServiceConfig)).To(Succeed())
		if err := cli.Create(ctx, inferenceServiceConfig); err != nil && !errors.IsAlreadyExists(err) {
			Fail(err.Error())
		}
	})

	When("deploying the first model in a namespace", func() {
		It("if the namespace is not part of the service mesh, it should enroll the namespace to the mesh", func() {
			inferenceService := createInferenceService(testNs, "")
			expectOwnedSmmCreated(inferenceService.Namespace)
		})

		It("if the namespace is already enrolled to the service mesh by the user, it should not modify the enrollment", func() {
			smm := createUserOwnedMeshEnrolment(testNs)
			inferenceService := createInferenceService(testNs, "")

			Consistently(func() bool {
				actualSmm := &maistrav1.ServiceMeshMember{}
				key := types.NamespacedName{Name: constants.ServiceMeshMemberName, Namespace: inferenceService.Namespace}
				err := cli.Get(ctx, key, actualSmm)
				return err == nil && reflect.DeepEqual(actualSmm, smm)
			}).Should(BeTrue())
		})

		It("if the namespace is already enrolled to some other control plane, it should anyway not modify the enrollment", func() {
			smm := &maistrav1.ServiceMeshMember{
				ObjectMeta: metav1.ObjectMeta{
					Name:        constants.ServiceMeshMemberName,
					Namespace:   testNs,
					Labels:      nil,
					Annotations: nil,
				},
				Spec: maistrav1.ServiceMeshMemberSpec{
					ControlPlaneRef: maistrav1.ServiceMeshControlPlaneRef{
						Name:      "random-control-plane-vbfr238497",
						Namespace: "random-namespace-a234h",
					}},
				Status: maistrav1.ServiceMeshMemberStatus{},
			}
			Expect(cli.Create(ctx, smm)).Should(Succeed())

			inferenceService := createInferenceService(testNs, "")

			Consistently(func() bool {
				actualSmm := &maistrav1.ServiceMeshMember{}
				key := types.NamespacedName{Name: constants.ServiceMeshMemberName, Namespace: inferenceService.Namespace}
				err := cli.Get(ctx, key, actualSmm)
				return err == nil && reflect.DeepEqual(actualSmm, smm)
			}).Should(BeTrue())
		})
	})

	When("deleting the last model in a namespace", func() {
		It("it should remove the owned service mesh enrolment", func() {
			inferenceService := createInferenceService(testNs, "")
			expectOwnedSmmCreated(inferenceService.Namespace)

			Expect(cli.Delete(ctx, inferenceService)).Should(Succeed())
			Eventually(func() error {
				smm := &maistrav1.ServiceMeshMember{}
				key := types.NamespacedName{Name: constants.ServiceMeshMemberName, Namespace: inferenceService.Namespace}
				err := cli.Get(ctx, key, smm)
				return err
			}, timeout, interval).ShouldNot(Succeed())
		})

		It("it should not remove a user-owned service mesh enrolment", func() {
			createUserOwnedMeshEnrolment(testNs)
			inferenceService := createInferenceService(testNs, "")

			Expect(cli.Delete(ctx, inferenceService)).Should(Succeed())
			Consistently(func() int {
				smmList := &maistrav1.ServiceMeshMemberList{}
				Expect(cli.List(ctx, smmList, client.InNamespace(inferenceService.Namespace))).Should(Succeed())
				return len(smmList.Items)
			}).Should(Equal(1))
		})
	})

	When("deleting a model, but there are other models left in the namespace", func() {
		It("it should not remove the owned service mesh enrolment", func() {
			inferenceService1 := createInferenceService(testNs, "")
			createInferenceService(testNs, "secondary-isvc")
			expectOwnedSmmCreated(inferenceService1.Namespace)

			Expect(cli.Delete(ctx, inferenceService1)).Should(Succeed())
			Consistently(func() error {
				smm := &maistrav1.ServiceMeshMember{}
				key := types.NamespacedName{Name: constants.ServiceMeshMemberName, Namespace: inferenceService1.Namespace}
				err := cli.Get(ctx, key, smm)
				return err
			}, timeout, interval).Should(Succeed())
		})
	})
})
