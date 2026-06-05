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

package v1alpha2

import (
	"context"
	"encoding/json"

	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"knative.dev/pkg/apis"

	"github.com/opendatahub-io/odh-model-controller/internal/webhook/connectionapi"
	testutils "github.com/opendatahub-io/odh-model-controller/test/utils"
)

const (
	llmNS            = "test-ns"
	llmISVCName      = "test-llmisvc"
	llmS3SecretName  = "s3-secret"
	llmURISecretName = "uri-secret"
	llmOCISecretName = "oci-secret"
	llmOldSecretName = "old-secret"
)

// newLLMScheme returns a scheme with core types needed by the fake client.
func newLLMScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(corev1.AddToScheme(s))
	return s
}

// newLLMFakeClient creates a fake client pre-loaded with objects.
func newLLMFakeClient(objs ...client.Object) client.Client {
	s := newLLMScheme()
	b := fake.NewClientBuilder().WithScheme(s)
	if len(objs) > 0 {
		b = b.WithObjects(objs...)
	}
	return b.Build()
}

// newLLMDefaulter instantiates LLMInferenceServiceCustomDefaulter with a fake client.
func newLLMDefaulter(cli client.Client) *LLMInferenceServiceCustomDefaulter {
	return &LLMInferenceServiceCustomDefaulter{
		client:    cli,
		apiReader: cli,
	}
}

// llmAdmissionCtx returns a context carrying an admission request.
func llmAdmissionCtx(op admissionv1.Operation, oldObj runtime.Object, dryRun bool) context.Context {
	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: op,
			Namespace: llmNS,
		},
	}
	if dryRun {
		t := true
		req.DryRun = &t
	}
	if oldObj != nil {
		raw, _ := json.Marshal(oldObj)
		req.OldObject = runtime.RawExtension{Raw: raw}
	}
	return admission.NewContextWithRequest(context.Background(), req)
}

// buildLLMISVC creates a typed LLMInferenceService for testing.
func buildLLMISVC(annotations map[string]string) *kservev1alpha2.LLMInferenceService {
	return &kservev1alpha2.LLMInferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Name:        llmISVCName,
			Namespace:   llmNS,
			Annotations: annotations,
		},
	}
}

// marshalSpec marshals the LLMInferenceService and returns spec as map[string]any.
func marshalSpec(llmisvc *kservev1alpha2.LLMInferenceService) map[string]any {
	raw, err := json.Marshal(llmisvc)
	Expect(err).ToNot(HaveOccurred())
	var m map[string]any
	Expect(json.Unmarshal(raw, &m)).To(Succeed())
	spec, ok := m["spec"].(map[string]any)
	Expect(ok).To(BeTrue(), "spec should be a map")
	return spec
}

var _ = Describe("LLMInferenceService ConnectionsAPI Defaulter", func() {

	Describe("CREATE operations", func() {

		It("skips injection when no connection annotation is set", func() {
			cli := newLLMFakeClient()
			d := newLLMDefaulter(cli)
			llmisvc := buildLLMISVC(nil)
			admCtx := llmAdmissionCtx(admissionv1.Create, nil, false)

			Expect(d.Default(admCtx, llmisvc)).To(Succeed())
			Expect(llmisvc.Spec.Model.URI.String()).To(BeEmpty())
			Expect(llmisvc.Spec.Template).To(BeNil())
		})

		It("skips injection when secret has no connection type annotation", func() {
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "secret1", Namespace: llmNS},
			}
			cli := newLLMFakeClient(secret)
			d := newLLMDefaulter(cli)
			llmisvc := buildLLMISVC(map[string]string{
				connectionapi.AnnotationConnections: "secret1",
			})
			admCtx := llmAdmissionCtx(admissionv1.Create, nil, false)

			Expect(d.Default(admCtx, llmisvc)).To(Succeed())
			Expect(llmisvc.Spec.Model.URI.String()).To(BeEmpty())
		})

		It("skips injection when secret has an unknown connection type", func() {
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "secret1",
					Namespace: llmNS,
					Annotations: map[string]string{
						connectionapi.AnnotationConnectionTypeProtocol: "exotic",
					},
				},
			}
			cli := newLLMFakeClient(secret)
			d := newLLMDefaulter(cli)
			llmisvc := buildLLMISVC(map[string]string{
				connectionapi.AnnotationConnections: "secret1",
			})
			admCtx := llmAdmissionCtx(admissionv1.Create, nil, false)

			Expect(d.Default(admCtx, llmisvc)).To(Succeed())
			Expect(llmisvc.Spec.Model.URI.String()).To(BeEmpty())
		})

		It("returns error when referenced secret does not exist", func() {
			cli := newLLMFakeClient()
			d := newLLMDefaulter(cli)
			llmisvc := buildLLMISVC(map[string]string{
				connectionapi.AnnotationConnections: "missing-secret",
			})
			admCtx := llmAdmissionCtx(admissionv1.Create, nil, false)

			err := d.Default(admCtx, llmisvc)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found"))
		})

		It("skips injection when resource is marked for deletion", func() {
			secret := testutils.BuildSecret(llmS3SecretName, llmNS, "s3",
				map[string][]byte{"AWS_S3_BUCKET": []byte("my-bucket")})
			cli := newLLMFakeClient(secret)
			d := newLLMDefaulter(cli)
			llmisvc := buildLLMISVC(map[string]string{
				connectionapi.AnnotationConnections: llmS3SecretName,
			})
			now := metav1.Now()
			llmisvc.DeletionTimestamp = &now
			admCtx := llmAdmissionCtx(admissionv1.Create, nil, false)

			Expect(d.Default(admCtx, llmisvc)).To(Succeed())
			Expect(llmisvc.Spec.Template).To(BeNil())
		})

		Context("S3 connection type", func() {

			It("creates SA and injects spec.model.uri", func() {
				secret := testutils.BuildSecret(llmS3SecretName, llmNS, "s3",
					map[string][]byte{"AWS_S3_BUCKET": []byte("my-bucket")})
				cli := newLLMFakeClient(secret)
				d := newLLMDefaulter(cli)
				llmisvc := buildLLMISVC(map[string]string{
					connectionapi.AnnotationConnections:    llmS3SecretName,
					connectionapi.AnnotationConnectionPath: "models/llama",
				})
				admCtx := llmAdmissionCtx(admissionv1.Create, nil, false)

				Expect(d.Default(admCtx, llmisvc)).To(Succeed())
				Expect(llmisvc.Spec.Template).ToNot(BeNil())
				Expect(llmisvc.Spec.Template.ServiceAccountName).To(Equal(llmS3SecretName + "-sa"))
				Expect(llmisvc.Spec.Model.URI.String()).To(Equal("s3://my-bucket/models/llama"))

				sa := &corev1.ServiceAccount{}
				Expect(cli.Get(context.Background(), types.NamespacedName{
					Name:      llmS3SecretName + "-sa",
					Namespace: llmNS,
				}, sa)).To(Succeed())
			})

			It("initializes Template when nil before S3 inject", func() {
				secret := testutils.BuildSecret(llmS3SecretName, llmNS, "s3",
					map[string][]byte{"AWS_S3_BUCKET": []byte("my-bucket")})
				cli := newLLMFakeClient(secret)
				d := newLLMDefaulter(cli)
				llmisvc := buildLLMISVC(map[string]string{
					connectionapi.AnnotationConnections:    llmS3SecretName,
					connectionapi.AnnotationConnectionPath: "models/llama",
				})
				Expect(llmisvc.Spec.Template).To(BeNil())
				admCtx := llmAdmissionCtx(admissionv1.Create, nil, false)

				Expect(d.Default(admCtx, llmisvc)).To(Succeed())
				Expect(llmisvc.Spec.Template).ToNot(BeNil())
				Expect(llmisvc.Spec.Template.ServiceAccountName).To(Equal(llmS3SecretName + "-sa"))
			})

			It("injects URI but does not create SA on dry-run", func() {
				secret := testutils.BuildSecret(llmS3SecretName, llmNS, "s3",
					map[string][]byte{"AWS_S3_BUCKET": []byte("my-bucket")})
				cli := newLLMFakeClient(secret)
				d := newLLMDefaulter(cli)
				llmisvc := buildLLMISVC(map[string]string{
					connectionapi.AnnotationConnections:    llmS3SecretName,
					connectionapi.AnnotationConnectionPath: "models/llama",
				})
				admCtx := llmAdmissionCtx(admissionv1.Create, nil, true) // dryRun=true

				Expect(d.Default(admCtx, llmisvc)).To(Succeed())
				Expect(llmisvc.Spec.Model.URI.String()).To(Equal("s3://my-bucket/models/llama"))

				sa := &corev1.ServiceAccount{}
				err := cli.Get(context.Background(), types.NamespacedName{
					Name:      llmS3SecretName + "-sa",
					Namespace: llmNS,
				}, sa)
				Expect(err).To(HaveOccurred())
			})

			It("returns error when bucket key is missing", func() {
				secret := testutils.BuildSecret(llmS3SecretName, llmNS, "s3", map[string][]byte{})
				cli := newLLMFakeClient(secret)
				d := newLLMDefaulter(cli)
				llmisvc := buildLLMISVC(map[string]string{
					connectionapi.AnnotationConnections:    llmS3SecretName,
					connectionapi.AnnotationConnectionPath: "models/v1",
				})
				admCtx := llmAdmissionCtx(admissionv1.Create, nil, false)

				err := d.Default(admCtx, llmisvc)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("AWS_S3_BUCKET"))
			})

			It("returns error when connection-path annotation is missing", func() {
				secret := testutils.BuildSecret(llmS3SecretName, llmNS, "s3",
					map[string][]byte{"AWS_S3_BUCKET": []byte("b")})
				cli := newLLMFakeClient(secret)
				d := newLLMDefaulter(cli)
				llmisvc := buildLLMISVC(map[string]string{
					connectionapi.AnnotationConnections: llmS3SecretName,
					// no AnnotationConnectionPath
				})
				admCtx := llmAdmissionCtx(admissionv1.Create, nil, false)

				err := d.Default(admCtx, llmisvc)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("path"))
			})
		})

		Context("URI connection type", func() {

			It("injects spec.model.uri from https-host key", func() {
				secret := testutils.BuildSecret(llmURISecretName, llmNS, "uri",
					map[string][]byte{"https-host": []byte("https://hf.co/model")})
				cli := newLLMFakeClient(secret)
				d := newLLMDefaulter(cli)
				llmisvc := buildLLMISVC(map[string]string{
					connectionapi.AnnotationConnections: llmURISecretName,
				})
				admCtx := llmAdmissionCtx(admissionv1.Create, nil, false)

				Expect(d.Default(admCtx, llmisvc)).To(Succeed())
				Expect(llmisvc.Spec.Model.URI.String()).To(Equal("https://hf.co/model"))
			})

			It("injects spec.model.uri from URI key", func() {
				secret := testutils.BuildSecret(llmURISecretName, llmNS, "uri",
					map[string][]byte{"URI": []byte("hf://facebook/model")})
				cli := newLLMFakeClient(secret)
				d := newLLMDefaulter(cli)
				llmisvc := buildLLMISVC(map[string]string{
					connectionapi.AnnotationConnections: llmURISecretName,
				})
				admCtx := llmAdmissionCtx(admissionv1.Create, nil, false)

				Expect(d.Default(admCtx, llmisvc)).To(Succeed())
				Expect(llmisvc.Spec.Model.URI.String()).To(Equal("hf://facebook/model"))
			})

			It("returns error when secret has neither URI key", func() {
				secret := testutils.BuildSecret(llmURISecretName, llmNS, "uri", map[string][]byte{})
				cli := newLLMFakeClient(secret)
				d := newLLMDefaulter(cli)
				llmisvc := buildLLMISVC(map[string]string{
					connectionapi.AnnotationConnections: llmURISecretName,
				})
				admCtx := llmAdmissionCtx(admissionv1.Create, nil, false)

				err := d.Default(admCtx, llmisvc)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("URI"))
			})
		})

		Context("OCI connection type", func() {

			It("injects imagePullSecrets", func() {
				secret := testutils.BuildSecret(llmOCISecretName, llmNS, "oci", nil)
				cli := newLLMFakeClient(secret)
				d := newLLMDefaulter(cli)
				llmisvc := buildLLMISVC(map[string]string{
					connectionapi.AnnotationConnections: llmOCISecretName,
				})
				admCtx := llmAdmissionCtx(admissionv1.Create, nil, false)

				Expect(d.Default(admCtx, llmisvc)).To(Succeed())
				Expect(llmisvc.Spec.Template).ToNot(BeNil())
				Expect(llmisvc.Spec.Template.ImagePullSecrets).To(ConsistOf(
					corev1.LocalObjectReference{Name: llmOCISecretName},
				))
			})

			It("initializes Template when nil before OCI inject", func() {
				secret := testutils.BuildSecret(llmOCISecretName, llmNS, "oci", nil)
				cli := newLLMFakeClient(secret)
				d := newLLMDefaulter(cli)
				llmisvc := buildLLMISVC(map[string]string{
					connectionapi.AnnotationConnections: llmOCISecretName,
				})
				Expect(llmisvc.Spec.Template).To(BeNil())
				admCtx := llmAdmissionCtx(admissionv1.Create, nil, false)

				Expect(d.Default(admCtx, llmisvc)).To(Succeed())
				Expect(llmisvc.Spec.Template).ToNot(BeNil())
			})

			It("does not duplicate imagePullSecrets when already present", func() {
				secret := testutils.BuildSecret(llmOCISecretName, llmNS, "oci", nil)
				cli := newLLMFakeClient(secret)
				d := newLLMDefaulter(cli)
				llmisvc := buildLLMISVC(map[string]string{
					connectionapi.AnnotationConnections: llmOCISecretName,
				})
				llmisvc.Spec.Template = &corev1.PodSpec{
					ImagePullSecrets: []corev1.LocalObjectReference{{Name: llmOCISecretName}},
				}
				admCtx := llmAdmissionCtx(admissionv1.Create, nil, false)

				Expect(d.Default(admCtx, llmisvc)).To(Succeed())
				Expect(llmisvc.Spec.Template.ImagePullSecrets).To(HaveLen(1))
			})
		})
	})

	Describe("UPDATE operations — removal (annotation removed)", func() {

		It("clears SA and removes spec.model on S3 removal", func() {
			secret := testutils.BuildSecret(llmS3SecretName, llmNS, "s3",
				map[string][]byte{"AWS_S3_BUCKET": []byte("b")})
			cli := newLLMFakeClient(secret)
			d := newLLMDefaulter(cli)
			oldLLM := buildLLMISVC(map[string]string{
				connectionapi.AnnotationConnections: llmS3SecretName,
			})
			newLLM := buildLLMISVC(nil)
			newLLM.Spec.Template = &corev1.PodSpec{ServiceAccountName: llmS3SecretName + "-sa"}
			s3URI, parseErr := apis.ParseURL("s3://my-bucket/models")
			Expect(parseErr).ToNot(HaveOccurred())
			newLLM.Spec.Model.URI = *s3URI
			admCtx := llmAdmissionCtx(admissionv1.Update, oldLLM, false)

			Expect(d.Default(admCtx, newLLM)).To(Succeed())
			Expect(newLLM.Spec.Template.ServiceAccountName).To(BeEmpty())
			// After cleanup, Model.URI is zero
			Expect(newLLM.Spec.Model.URI.String()).To(BeEmpty())
		})

		It("removes spec.model on URI removal", func() {
			secret := testutils.BuildSecret(llmURISecretName, llmNS, "uri",
				map[string][]byte{"URI": []byte("https://x")})
			cli := newLLMFakeClient(secret)
			d := newLLMDefaulter(cli)
			oldLLM := buildLLMISVC(map[string]string{
				connectionapi.AnnotationConnections: llmURISecretName,
			})
			newLLM := buildLLMISVC(nil)
			preURI, parseErr := apis.ParseURL("https://x")
			Expect(parseErr).ToNot(HaveOccurred())
			newLLM.Spec.Model.URI = *preURI
			admCtx := llmAdmissionCtx(admissionv1.Update, oldLLM, false)

			Expect(d.Default(admCtx, newLLM)).To(Succeed())
			Expect(newLLM.Spec.Model.URI.String()).To(BeEmpty())
		})

		It("cleans up imagePullSecrets on OCI removal", func() {
			secret := testutils.BuildSecret(llmOCISecretName, llmNS, "oci", nil)
			cli := newLLMFakeClient(secret)
			d := newLLMDefaulter(cli)
			oldLLM := buildLLMISVC(map[string]string{
				connectionapi.AnnotationConnections: llmOCISecretName,
			})
			newLLM := buildLLMISVC(nil)
			newLLM.Spec.Template = &corev1.PodSpec{
				ImagePullSecrets: []corev1.LocalObjectReference{{Name: llmOCISecretName}},
			}
			admCtx := llmAdmissionCtx(admissionv1.Update, oldLLM, false)

			Expect(d.Default(admCtx, newLLM)).To(Succeed())
			Expect(newLLM.Spec.Template.ImagePullSecrets).To(BeEmpty())
		})

		It("clears entire imagePullSecrets when old SecretName is empty on OCI cleanup", func() {
			// Direct call: corner case not reachable through the normal webhook flow
			llmisvc := buildLLMISVC(nil)
			llmisvc.Spec.Template = &corev1.PodSpec{
				ImagePullSecrets: []corev1.LocalObjectReference{{Name: "a"}},
			}
			performLLMISVCCleanup(&llmisvc.Spec.Model.URI, &llmisvc.Spec.Template, connectionapi.ConnectionInfo{
				SecretName: "",
				Type:       connectionapi.ConnectionTypeProtocolOCI.String(),
			})
			Expect(llmisvc.Spec.Template.ImagePullSecrets).To(BeNil())
		})

		It("performs full cleanup when old secret has been deleted", func() {
			cli := newLLMFakeClient()
			d := newLLMDefaulter(cli)
			oldLLM := buildLLMISVC(map[string]string{
				connectionapi.AnnotationConnections: "deleted-secret",
			})
			newLLM := buildLLMISVC(nil)
			newLLM.Spec.Template = &corev1.PodSpec{
				ServiceAccountName: "deleted-secret-sa",
				ImagePullSecrets:   []corev1.LocalObjectReference{{Name: "deleted-secret"}},
			}
			admCtx := llmAdmissionCtx(admissionv1.Update, oldLLM, false)

			Expect(d.Default(admCtx, newLLM)).To(Succeed())
			Expect(newLLM.Spec.Template.ServiceAccountName).To(BeEmpty())
			Expect(newLLM.Spec.Model.URI.String()).To(BeEmpty())
		})

		It("preserves user-set SA name when removing S3 connection", func() {
			secret := testutils.BuildSecret(llmS3SecretName, llmNS, "s3",
				map[string][]byte{"AWS_S3_BUCKET": []byte("b")})
			cli := newLLMFakeClient(secret)
			d := newLLMDefaulter(cli)
			oldLLM := buildLLMISVC(map[string]string{
				connectionapi.AnnotationConnections: llmS3SecretName,
			})
			newLLM := buildLLMISVC(nil)
			newLLM.Spec.Template = &corev1.PodSpec{ServiceAccountName: "user-custom-sa"}
			admCtx := llmAdmissionCtx(admissionv1.Update, oldLLM, false)

			Expect(d.Default(admCtx, newLLM)).To(Succeed())
			Expect(newLLM.Spec.Template.ServiceAccountName).To(Equal("user-custom-sa"))
		})
	})

	Describe("UPDATE operations — replacement", func() {

		It("replaces URI with S3 when connection type changes", func() {
			uriSecret := testutils.BuildSecret(llmURISecretName, llmNS, "uri",
				map[string][]byte{"URI": []byte("https://x")})
			s3Secret := testutils.BuildSecret(llmS3SecretName, llmNS, "s3",
				map[string][]byte{"AWS_S3_BUCKET": []byte("b")})
			cli := newLLMFakeClient(uriSecret, s3Secret)
			d := newLLMDefaulter(cli)
			oldLLM := buildLLMISVC(map[string]string{
				connectionapi.AnnotationConnections: llmURISecretName,
			})
			newLLM := buildLLMISVC(map[string]string{
				connectionapi.AnnotationConnections:    llmS3SecretName,
				connectionapi.AnnotationConnectionPath: "models/v1",
			})
			admCtx := llmAdmissionCtx(admissionv1.Update, oldLLM, false)

			Expect(d.Default(admCtx, newLLM)).To(Succeed())
			Expect(newLLM.Spec.Template).ToNot(BeNil())
			Expect(newLLM.Spec.Template.ServiceAccountName).To(Equal(llmS3SecretName + "-sa"))
			Expect(newLLM.Spec.Model.URI.String()).To(Equal("s3://b/models/v1"))
		})

		It("replaces S3 fields when connection path changes", func() {
			secret := testutils.BuildSecret(llmS3SecretName, llmNS, "s3",
				map[string][]byte{"AWS_S3_BUCKET": []byte("b")})
			cli := newLLMFakeClient(secret)
			d := newLLMDefaulter(cli)
			oldLLM := buildLLMISVC(map[string]string{
				connectionapi.AnnotationConnections:    llmS3SecretName,
				connectionapi.AnnotationConnectionPath: "old",
			})
			newLLM := buildLLMISVC(map[string]string{
				connectionapi.AnnotationConnections:    llmS3SecretName,
				connectionapi.AnnotationConnectionPath: "new",
			})
			admCtx := llmAdmissionCtx(admissionv1.Update, oldLLM, false)

			Expect(d.Default(admCtx, newLLM)).To(Succeed())
			Expect(newLLM.Spec.Model.URI.String()).To(Equal("s3://b/new"))
		})
	})

	Describe("removeModelSpec verification", func() {

		It("zeros spec.model.uri after removal", func() {
			// Trigger cleanup via Update removal
			secret := testutils.BuildSecret(llmURISecretName, llmNS, "uri",
				map[string][]byte{"URI": []byte("https://hf.co/model")})
			cli := newLLMFakeClient(secret)
			d := newLLMDefaulter(cli)
			oldLLM := buildLLMISVC(map[string]string{
				connectionapi.AnnotationConnections: llmURISecretName,
			})
			newLLM := buildLLMISVC(nil)
			preURI, parseErr := apis.ParseURL("https://hf.co/model")
			Expect(parseErr).ToNot(HaveOccurred())
			newLLM.Spec.Model.URI = *preURI
			admCtx := llmAdmissionCtx(admissionv1.Update, oldLLM, false)

			Expect(d.Default(admCtx, newLLM)).To(Succeed())
			// After cleanup, Model.URI is zero-valued
			Expect(newLLM.Spec.Model.URI.String()).To(BeEmpty())
		})

		It("preserves other spec fields after model removal", func() {
			secret := testutils.BuildSecret(llmURISecretName, llmNS, "uri",
				map[string][]byte{"URI": []byte("https://hf.co/model")})
			cli := newLLMFakeClient(secret)
			d := newLLMDefaulter(cli)
			oldLLM := buildLLMISVC(map[string]string{
				connectionapi.AnnotationConnections: llmURISecretName,
			})
			newLLM := buildLLMISVC(nil)
			newLLM.Spec.Template = &corev1.PodSpec{ServiceAccountName: "some-sa"}
			preURI, parseErr := apis.ParseURL("https://hf.co/model")
			Expect(parseErr).ToNot(HaveOccurred())
			newLLM.Spec.Model.URI = *preURI
			admCtx := llmAdmissionCtx(admissionv1.Update, oldLLM, false)

			Expect(d.Default(admCtx, newLLM)).To(Succeed())
			// Model URI cleared but Template preserved
			Expect(newLLM.Spec.Model.URI.String()).To(BeEmpty())
			Expect(newLLM.Spec.Template).ToNot(BeNil())
			Expect(newLLM.Spec.Template.ServiceAccountName).To(Equal("some-sa"))
		})
	})

	Describe("Backward compatibility (deprecated connection-type-ref)", func() {

		It("injects S3 fields using connection-type-ref: s3", func() {
			secret := testutils.BuildSecretWithRef(llmS3SecretName, llmNS, "s3",
				map[string][]byte{"AWS_S3_BUCKET": []byte("b")})
			cli := newLLMFakeClient(secret)
			d := newLLMDefaulter(cli)
			llmisvc := buildLLMISVC(map[string]string{
				connectionapi.AnnotationConnections:    llmS3SecretName,
				connectionapi.AnnotationConnectionPath: "m",
			})
			admCtx := llmAdmissionCtx(admissionv1.Create, nil, false)

			Expect(d.Default(admCtx, llmisvc)).To(Succeed())
			Expect(llmisvc.Spec.Template.ServiceAccountName).To(Equal(llmS3SecretName + "-sa"))
			Expect(llmisvc.Spec.Model.URI.String()).To(Equal("s3://b/m"))
		})

		It("injects spec.model.uri using connection-type-ref: uri-v1", func() {
			secret := testutils.BuildSecretWithRef(llmURISecretName, llmNS, "uri-v1",
				map[string][]byte{"URI": []byte("https://x")})
			cli := newLLMFakeClient(secret)
			d := newLLMDefaulter(cli)
			llmisvc := buildLLMISVC(map[string]string{
				connectionapi.AnnotationConnections: llmURISecretName,
			})
			admCtx := llmAdmissionCtx(admissionv1.Create, nil, false)

			Expect(d.Default(admCtx, llmisvc)).To(Succeed())
			Expect(llmisvc.Spec.Model.URI.String()).To(Equal("https://x"))
		})

		It("injects imagePullSecrets using connection-type-ref: oci-v1", func() {
			secret := testutils.BuildSecretWithRef(llmOCISecretName, llmNS, "oci-v1", nil)
			cli := newLLMFakeClient(secret)
			d := newLLMDefaulter(cli)
			llmisvc := buildLLMISVC(map[string]string{
				connectionapi.AnnotationConnections: llmOCISecretName,
			})
			admCtx := llmAdmissionCtx(admissionv1.Create, nil, false)

			Expect(d.Default(admCtx, llmisvc)).To(Succeed())
			Expect(llmisvc.Spec.Template.ImagePullSecrets).To(ConsistOf(
				corev1.LocalObjectReference{Name: llmOCISecretName},
			))
		})
	})

	Describe("Utility edge cases exercised through the webhook", func() {

		It("prefers https-host over URI key when both present", func() {
			secret := testutils.BuildSecret(llmURISecretName, llmNS, "uri", map[string][]byte{
				"https-host": []byte("https://preferred.com"),
				"URI":        []byte("https://fallback.com"),
			})
			cli := newLLMFakeClient(secret)
			d := newLLMDefaulter(cli)
			llmisvc := buildLLMISVC(map[string]string{
				connectionapi.AnnotationConnections: llmURISecretName,
			})
			admCtx := llmAdmissionCtx(admissionv1.Create, nil, false)

			Expect(d.Default(admCtx, llmisvc)).To(Succeed())
			Expect(llmisvc.Spec.Model.URI.String()).To(Equal("https://preferred.com"))
		})

		It("returns error when bucket value is empty", func() {
			secret := testutils.BuildSecret(llmS3SecretName, llmNS, "s3",
				map[string][]byte{"AWS_S3_BUCKET": []byte("")})
			cli := newLLMFakeClient(secret)
			d := newLLMDefaulter(cli)
			llmisvc := buildLLMISVC(map[string]string{
				connectionapi.AnnotationConnections:    llmS3SecretName,
				connectionapi.AnnotationConnectionPath: "models/v1",
			})
			admCtx := llmAdmissionCtx(admissionv1.Create, nil, false)

			err := d.Default(admCtx, llmisvc)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("empty"))
		})

		It("does not trigger replacement when URI path annotation changes", func() {
			secret := testutils.BuildSecret(llmURISecretName, llmNS, "uri",
				map[string][]byte{"URI": []byte("https://x")})
			cli := newLLMFakeClient(secret)
			d := newLLMDefaulter(cli)
			oldLLM := buildLLMISVC(map[string]string{
				connectionapi.AnnotationConnections:    llmURISecretName,
				connectionapi.AnnotationConnectionPath: "old",
			})
			newLLM := buildLLMISVC(map[string]string{
				connectionapi.AnnotationConnections:    llmURISecretName,
				connectionapi.AnnotationConnectionPath: "new",
			})
			admCtx := llmAdmissionCtx(admissionv1.Update, oldLLM, false)

			// action=none for URI when only path annotation changes
			Expect(d.Default(admCtx, newLLM)).To(Succeed())
			// No injection triggered; Model.URI unchanged (stays zero since no inject)
			Expect(newLLM.Spec.Model.URI.String()).To(BeEmpty())
		})

		It("cleans up imagePullSecrets by name when old secret is deleted", func() {
			cli := newLLMFakeClient()
			d := newLLMDefaulter(cli)
			oldLLM := buildLLMISVC(map[string]string{
				connectionapi.AnnotationConnections: "deleted-secret",
			})
			newLLM := buildLLMISVC(nil)
			newLLM.Spec.Template = &corev1.PodSpec{
				ImagePullSecrets: []corev1.LocalObjectReference{
					{Name: "deleted-secret"},
					{Name: "other"},
				},
			}
			admCtx := llmAdmissionCtx(admissionv1.Update, oldLLM, false)

			Expect(d.Default(admCtx, newLLM)).To(Succeed())
			// Unknown type: cleanup by name removes "deleted-secret", preserves "other"
			Expect(newLLM.Spec.Template.ImagePullSecrets).To(ConsistOf(
				corev1.LocalObjectReference{Name: "other"},
			))
		})

		It("clears entire imagePullSecrets when old SecretName is empty", func() {
			// Direct call: corner case unreachable through normal webhook flow
			llmisvc := buildLLMISVC(nil)
			llmisvc.Spec.Template = &corev1.PodSpec{
				ImagePullSecrets: []corev1.LocalObjectReference{{Name: "a"}},
			}
			performLLMISVCCleanup(&llmisvc.Spec.Model.URI, &llmisvc.Spec.Template, connectionapi.ConnectionInfo{
				SecretName: "",
				Type:       "",
			})
			Expect(llmisvc.Spec.Template.ImagePullSecrets).To(BeNil())
		})

		It("prefers protocol annotation over deprecated ref on Secret", func() {
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "secret1",
					Namespace: llmNS,
					Annotations: map[string]string{
						connectionapi.AnnotationConnectionTypeProtocol: "s3",
						connectionapi.AnnotationConnectionTypeRef:      "oci-v1",
					},
				},
				Data: map[string][]byte{"AWS_S3_BUCKET": []byte("b")},
			}
			cli := newLLMFakeClient(secret)
			d := newLLMDefaulter(cli)
			llmisvc := buildLLMISVC(map[string]string{
				connectionapi.AnnotationConnections:    "secret1",
				connectionapi.AnnotationConnectionPath: "m",
			})
			admCtx := llmAdmissionCtx(admissionv1.Create, nil, false)

			Expect(d.Default(admCtx, llmisvc)).To(Succeed())
			// S3 injection applied (not OCI)
			Expect(llmisvc.Spec.Template.ServiceAccountName).To(Equal("secret1-sa"))
			Expect(llmisvc.Spec.Model.URI.String()).To(Equal("s3://b/m"))
			Expect(llmisvc.Spec.Template.ImagePullSecrets).To(BeEmpty())
		})

		It("succeeds when SA already exists (idempotent creation)", func() {
			secret := testutils.BuildSecret(llmS3SecretName, llmNS, "s3",
				map[string][]byte{"AWS_S3_BUCKET": []byte("b")})
			existingSA := &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      llmS3SecretName + "-sa",
					Namespace: llmNS,
				},
			}
			cli := newLLMFakeClient(secret, existingSA)
			d := newLLMDefaulter(cli)
			llmisvc := buildLLMISVC(map[string]string{
				connectionapi.AnnotationConnections:    llmS3SecretName,
				connectionapi.AnnotationConnectionPath: "models/v1",
			})
			admCtx := llmAdmissionCtx(admissionv1.Create, nil, false)

			Expect(d.Default(admCtx, llmisvc)).To(Succeed())
			Expect(llmisvc.Spec.Template.ServiceAccountName).To(Equal(llmS3SecretName + "-sa"))
		})

		It("does not overwrite user-set SA name on S3 CREATE", func() {
			secret := testutils.BuildSecret(llmS3SecretName, llmNS, "s3",
				map[string][]byte{"AWS_S3_BUCKET": []byte("b")})
			cli := newLLMFakeClient(secret)
			d := newLLMDefaulter(cli)
			llmisvc := buildLLMISVC(map[string]string{
				connectionapi.AnnotationConnections:    llmS3SecretName,
				connectionapi.AnnotationConnectionPath: "models/v1",
			})
			llmisvc.Spec.Template = &corev1.PodSpec{ServiceAccountName: "user-custom-sa"}
			admCtx := llmAdmissionCtx(admissionv1.Create, nil, false)

			Expect(d.Default(admCtx, llmisvc)).To(Succeed())
			Expect(llmisvc.Spec.Template.ServiceAccountName).To(Equal("user-custom-sa"))
			Expect(llmisvc.Spec.Model.URI.String()).To(Equal("s3://b/models/v1"))
		})

		It("merges OCI imagePullSecrets with pre-existing different entries", func() {
			secret := testutils.BuildSecret(llmOCISecretName, llmNS, "oci", nil)
			cli := newLLMFakeClient(secret)
			d := newLLMDefaulter(cli)
			llmisvc := buildLLMISVC(map[string]string{
				connectionapi.AnnotationConnections: llmOCISecretName,
			})
			llmisvc.Spec.Template = &corev1.PodSpec{
				ImagePullSecrets: []corev1.LocalObjectReference{{Name: "existing-secret"}},
			}
			admCtx := llmAdmissionCtx(admissionv1.Create, nil, false)

			Expect(d.Default(admCtx, llmisvc)).To(Succeed())
			Expect(llmisvc.Spec.Template.ImagePullSecrets).To(ConsistOf(
				corev1.LocalObjectReference{Name: "existing-secret"},
				corev1.LocalObjectReference{Name: llmOCISecretName},
			))
		})

		It("preserves other imagePullSecrets entries on OCI removal", func() {
			secret := testutils.BuildSecret(llmOCISecretName, llmNS, "oci", nil)
			cli := newLLMFakeClient(secret)
			d := newLLMDefaulter(cli)
			oldLLM := buildLLMISVC(map[string]string{
				connectionapi.AnnotationConnections: llmOCISecretName,
			})
			newLLM := buildLLMISVC(nil)
			newLLM.Spec.Template = &corev1.PodSpec{
				ImagePullSecrets: []corev1.LocalObjectReference{
					{Name: "other"},
					{Name: llmOCISecretName},
				},
			}
			admCtx := llmAdmissionCtx(admissionv1.Update, oldLLM, false)

			Expect(d.Default(admCtx, newLLM)).To(Succeed())
			Expect(newLLM.Spec.Template.ImagePullSecrets).To(ConsistOf(
				corev1.LocalObjectReference{Name: "other"},
			))
		})

		It("produces no mutation when neither old nor new has annotation", func() {
			cli := newLLMFakeClient()
			d := newLLMDefaulter(cli)
			oldLLM := buildLLMISVC(nil)
			newLLM := buildLLMISVC(nil)
			admCtx := llmAdmissionCtx(admissionv1.Update, oldLLM, false)

			Expect(d.Default(admCtx, newLLM)).To(Succeed())
			Expect(newLLM.Spec.Template).To(BeNil())
			Expect(newLLM.Spec.Model.URI.String()).To(BeEmpty())
		})

		It("triggers replacement when same S3 type but different secret name", func() {
			oldSecret := testutils.BuildSecret(llmOldSecretName, llmNS, "s3",
				map[string][]byte{"AWS_S3_BUCKET": []byte("b")})
			newSecret := testutils.BuildSecret(llmS3SecretName, llmNS, "s3",
				map[string][]byte{"AWS_S3_BUCKET": []byte("b")})
			cli := newLLMFakeClient(oldSecret, newSecret)
			d := newLLMDefaulter(cli)
			oldLLM := buildLLMISVC(map[string]string{
				connectionapi.AnnotationConnections:    llmOldSecretName,
				connectionapi.AnnotationConnectionPath: "models/v1",
			})
			newLLM := buildLLMISVC(map[string]string{
				connectionapi.AnnotationConnections:    llmS3SecretName,
				connectionapi.AnnotationConnectionPath: "models/v1",
			})
			newLLM.Spec.Template = &corev1.PodSpec{ServiceAccountName: llmOldSecretName + "-sa"}
			admCtx := llmAdmissionCtx(admissionv1.Update, oldLLM, false)

			Expect(d.Default(admCtx, newLLM)).To(Succeed())
			Expect(newLLM.Spec.Template.ServiceAccountName).To(Equal(llmS3SecretName + "-sa"))
			Expect(newLLM.Spec.Model.URI.String()).To(Equal("s3://b/models/v1"))
		})

		It("produces no mutation for identical S3 connection", func() {
			secret := testutils.BuildSecret(llmS3SecretName, llmNS, "s3",
				map[string][]byte{"AWS_S3_BUCKET": []byte("b")})
			cli := newLLMFakeClient(secret)
			d := newLLMDefaulter(cli)
			annots := map[string]string{
				connectionapi.AnnotationConnections:    llmS3SecretName,
				connectionapi.AnnotationConnectionPath: "models/v1",
			}
			oldLLM := buildLLMISVC(annots)
			oldLLM.Spec.Template = &corev1.PodSpec{ServiceAccountName: llmS3SecretName + "-sa"}
			newLLM := buildLLMISVC(annots)
			newLLM.Spec.Template = &corev1.PodSpec{ServiceAccountName: llmS3SecretName + "-sa"}
			admCtx := llmAdmissionCtx(admissionv1.Update, oldLLM, false)

			// action=none: no change
			Expect(d.Default(admCtx, newLLM)).To(Succeed())
			Expect(newLLM.Spec.Template.ServiceAccountName).To(Equal(llmS3SecretName + "-sa"))
		})

		// Ensure marshalSpec helper works to prevent regressions
		It("marshalSpec helper returns a usable spec map", func() {
			llmisvc := buildLLMISVC(nil)
			spec := marshalSpec(llmisvc)
			Expect(spec).ToNot(BeNil())
		})
	})
})
