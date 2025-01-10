package pod

import (
	"context"
	"encoding/json"

	jsonpatch "github.com/evanphx/json-patch"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

type FakeDecoder struct{}

func (d *FakeDecoder) Decode(req admission.Request, obj runtime.Object) error {
	return json.Unmarshal(req.Object.Raw, obj)
}

// DecodeRaw implements the admission.Decoder interface
func (d *FakeDecoder) DecodeRaw(raw runtime.RawExtension, obj runtime.Object) error {
	return json.Unmarshal(raw.Raw, obj)
}

var _ = Describe("Pod Mutator Webhook", func() {
	var (
		mutator *Mutator
		decoder admission.Decoder
		ctx     context.Context
	)

	BeforeEach(func() {
		decoder = &FakeDecoder{}
		mutator = &Mutator{
			Decoder: decoder,
		}
		ctx = context.TODO()
	})

	Describe("Handle method", func() {
		It("should add ray-tls-generator init-container to the pod if RAY_USE_TLS is set to 1", func() {
			// Create a fake pod with RAY_USE_TLS environment variable
			fakePod := &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "worker-container",
							Env: []corev1.EnvVar{
								{Name: "RAY_USE_TLS", Value: "1"},
							},
						},
					},
				},
			}
			fakePodBytes, err := json.Marshal(fakePod)
			Expect(err).ToNot(HaveOccurred())

			// Create a fake admission request
			req := admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					UID:       uuid.NewUUID(),
					Operation: admissionv1.Create,
					Object: runtime.RawExtension{
						Raw: fakePodBytes,
					},
					Namespace: "test-namespace",
				},
			}

			// Call the Handle method
			resp := mutator.Handle(ctx, req)

			// Assertions
			Expect(resp.Allowed).To(BeTrue())
			Expect(resp.Patches).ToNot(BeNil())

			// Verify that the InitContainer was added
			patchBytes, err := json.Marshal(resp.Patches)
			Expect(err).NotTo(HaveOccurred())

			patch, err := jsonpatch.DecodePatch(patchBytes)
			Expect(err).NotTo(HaveOccurred())

			originalJSON, err := json.Marshal(fakePod)
			Expect(err).NotTo(HaveOccurred())

			// Step 4: Apply the patch to the original object
			mutatedJSON, err := patch.Apply(originalJSON)
			Expect(err).NotTo(HaveOccurred())

			// Step 5: Unmarshal the mutated JSON into an object
			mutatedPod := &corev1.Pod{}
			err = json.Unmarshal(mutatedJSON, &mutatedPod)
			Expect(err).NotTo(HaveOccurred())

			Expect(mutatedPod.Spec.InitContainers[0].Name).To(Equal(constants.RayTLSGeneratorInitContainerName))
		})
		It("should return true if RAY_USE_TLS is set to 1", func() {
			pod := &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "worker-container",
							Env: []corev1.EnvVar{
								{Name: "RAY_USE_TLS", Value: "1"},
							},
						},
					},
				},
			}
			result := needToAddRayTLSGenerator(pod)
			Expect(result).To(BeTrue())
		})

		It("should return true if RAY_USE_TLS is set to 0", func() {
			pod := &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "worker-container",
							Env: []corev1.EnvVar{
								{Name: "RAY_USE_TLS", Value: "0"},
							},
						},
					},
				},
			}
			result := needToAddRayTLSGenerator(pod)
			Expect(result).To(BeFalse())
		})

		It("should return false if RAY_USE_TLS is not set", func() {
			pod := &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "worker-container",
							Env:  []corev1.EnvVar{},
						},
					},
				},
			}
			result := needToAddRayTLSGenerator(pod)
			Expect(result).To(BeFalse())
		})
	})
})
