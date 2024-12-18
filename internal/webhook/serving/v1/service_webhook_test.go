/*
Copyright 2024.

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

package v1

import (
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	knservingv1 "knative.dev/serving/pkg/apis/serving/v1"
	v1 "maistra.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

var _ = Describe("Knative Service Webhook", func() {
	var validator admission.CustomValidator
	var meshNamespace string

	createKserveOwnedKsvc := func() *knservingv1.Service {
		return &knservingv1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ksvc",
				Namespace: "ns",
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: kservev1beta1.SchemeGroupVersion.String(),
						Kind:       "InferenceService",
						Name:       "myISVC",
					},
				},
			},
			Spec: knservingv1.ServiceSpec{},
		}
	}

	createSmmr := func(smmrStatus v1.ServiceMeshMemberRollStatus) *v1.ServiceMeshMemberRoll {
		smmr := &v1.ServiceMeshMemberRoll{
			ObjectMeta: metav1.ObjectMeta{
				Name:      constants.ServiceMeshMemberRollName,
				Namespace: meshNamespace,
			},
		}

		Expect(k8sClient.Create(ctx, smmr)).To(Succeed())

		smmr.Status = smmrStatus
		Expect(k8sClient.Status().Update(ctx, smmr)).To(Succeed())

		return smmr
	}

	BeforeEach(func() {
		_, meshNamespace = utils.GetIstioControlPlaneName(ctx, k8sClient)
		validator = &ServiceCustomValidator{client: k8sClient}

		// Other tests may create a ServiceMeshMemberRoll.
		// If there is one, delete it because it conflicts with tests in this file.
		smmr := v1.ServiceMeshMemberRoll{}
		getErr := k8sClient.Get(ctx, types.NamespacedName{
			Namespace: meshNamespace,
			Name:      constants.ServiceMeshMemberRollName,
		}, &smmr)
		if getErr != nil {
			if !errors.IsNotFound(getErr) {
				Fail("Error waiting for SMMR to be deleted: " + getErr.Error())
			}
		} else {
			Expect(k8sClient.Delete(ctx, &smmr)).ShouldNot(HaveOccurred())
		}
	})

	It("should accept creating a Knative service that does not have metadata", func() {
		ksvc := knservingv1.Service{
			TypeMeta:   metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{},
		}
		_, err := validator.ValidateCreate(ctx, &ksvc)
		Expect(err).ShouldNot(HaveOccurred())
	})

	It("should accept creating a Knative service that does not have owner references", func() {
		ksvc := knservingv1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "ksvc",
				Namespace:       "ns",
				OwnerReferences: nil,
			},
		}
		_, err := validator.ValidateCreate(ctx, &ksvc)
		Expect(err).ShouldNot(HaveOccurred())
	})

	It("should accept creating a Knative service that is now owned by KServe", func() {
		ksvc := knservingv1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ksvc",
				Namespace: "ns",
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: "Foo/v1",
						Kind:       "MyKind",
						Name:       "MyKindInstance",
						Controller: nil,
					},
				},
			},
		}

		_, err := validator.ValidateCreate(ctx, &ksvc)
		Expect(err).ShouldNot(HaveOccurred())
	})

	It("should accept creating a Knative service that is forced to not have an Istio sidecar", func() {
		ksvc := createKserveOwnedKsvc()
		ksvc.Spec.Template.ObjectMeta.Annotations = map[string]string{
			"sidecar.istio.io/inject": "false",
		}

		_, err := validator.ValidateCreate(ctx, ksvc)
		Expect(err).ShouldNot(HaveOccurred())
	})

	It("should reject creating a Knative service if there is no ServiceMeshMemberRoll yet", func() {
		ksvc := createKserveOwnedKsvc()
		_, err := validator.ValidateCreate(ctx, ksvc)
		Expect(err).Should(HaveOccurred())
	})

	It("should reject creating a Knative service if the ServiceMeshMemberRoll has null ConfiguredMembers", func() {
		ksvc := createKserveOwnedKsvc()
		smmr := createSmmr(v1.ServiceMeshMemberRollStatus{ConfiguredMembers: nil})
		defer func() { _ = k8sClient.Delete(ctx, smmr) }()

		_, err := validator.ValidateCreate(ctx, ksvc)
		Expect(err).Should(HaveOccurred())
	})

	It("should reject creating a Knative service if the namespace is not a configured member of the ServiceMeshMemberRoll", func() {
		ksvc := createKserveOwnedKsvc()
		smmr := createSmmr(v1.ServiceMeshMemberRollStatus{ConfiguredMembers: []string{"foo"}})
		defer func() { _ = k8sClient.Delete(ctx, smmr) }()

		_, err := validator.ValidateCreate(ctx, ksvc)
		Expect(err).Should(HaveOccurred())
	})

	It("should accept creating a Knative service if the namespace is a configured member of the ServiceMeshMemberRoll", func() {
		ksvc := createKserveOwnedKsvc()
		smmr := createSmmr(v1.ServiceMeshMemberRollStatus{ConfiguredMembers: []string{ksvc.Namespace}})
		defer func() { _ = k8sClient.Delete(ctx, smmr) }()

		_, err := validator.ValidateCreate(ctx, ksvc)
		Expect(err).ShouldNot(HaveOccurred())
	})

	It("should validate on update of Knative service regardless of the received objects", func() {
		_, err := validator.ValidateUpdate(ctx, nil, nil)
		Expect(err).ShouldNot(HaveOccurred())
	})

	It("should validate on deletion of Knative service regardless of the received object", func() {
		_, err := validator.ValidateDelete(ctx, nil)
		Expect(err).ShouldNot(HaveOccurred())
	})
})
