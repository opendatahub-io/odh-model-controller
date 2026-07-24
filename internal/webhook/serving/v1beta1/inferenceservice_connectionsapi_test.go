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

package v1beta1

import (
	"context"
	"encoding/json"

	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
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

	"github.com/opendatahub-io/odh-model-controller/internal/webhook/connectionapi"
	testutils "github.com/opendatahub-io/odh-model-controller/test/utils"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func newDefaulterScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(corev1.AddToScheme(s))
	return s
}

func newDefaulterFakeClient(objs ...client.Object) client.Client {
	b := fake.NewClientBuilder().WithScheme(newDefaulterScheme())
	if len(objs) > 0 {
		b = b.WithObjects(objs...)
	}
	return b.Build()
}

func newISVCDefaulter(cli client.Client) *InferenceServiceCustomDefaulter {
	return &InferenceServiceCustomDefaulter{client: cli, apiReader: cli}
}

// defaultCtx creates a context carrying an admission request for Default() calls.
func defaultCtx(op admissionv1.Operation, oldObj runtime.Object, dryRun bool) context.Context {
	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{Operation: op, Namespace: defaultNS},
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

// buildISVC creates a typed InferenceService for testing.
func buildISVC(annotations map[string]string) *kservev1beta1.InferenceService {
	return &kservev1beta1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: defaultNS, Annotations: annotations},
		Spec:       kservev1beta1.InferenceServiceSpec{Predictor: kservev1beta1.PredictorSpec{}},
	}
}

const (
	defaultNS       = "test-ns"
	defaultS3Secret = "s3-secret"
	defaultS3SA     = "s3-secret-sa"
)

// ── tests ─────────────────────────────────────────────────────────────────────

var _ = Describe("InferenceService ConnectionsAPI Defaulter", func() {

	Describe("CREATE operations", func() {

		It("skips injection when no connection annotation is set", func() {
			isvc := buildISVC(nil)
			Expect(newISVCDefaulter(newDefaulterFakeClient()).Default(defaultCtx(admissionv1.Create, nil, false), isvc)).To(Succeed())
			Expect(isvc.Spec.Predictor.ServiceAccountName).To(BeEmpty())
			Expect(isvc.Spec.Predictor.Model).To(BeNil())
			Expect(isvc.Spec.Predictor.ImagePullSecrets).To(BeEmpty())
		})

		It("returns error when secret has no connection type annotation", func() {
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: defaultNS}}
			isvc := buildISVC(map[string]string{connectionapi.AnnotationConnections: "s"})
			err := newISVCDefaulter(newDefaulterFakeClient(secret)).Default(defaultCtx(admissionv1.Create, nil, false), isvc)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(connectionapi.AnnotationConnectionTypeProtocol))
		})

		It("returns error when secret has an unknown connection type", func() {
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "s", Namespace: defaultNS,
					Annotations: map[string]string{connectionapi.AnnotationConnectionTypeProtocol: "exotic"},
				},
			}
			isvc := buildISVC(map[string]string{connectionapi.AnnotationConnections: "s"})
			err := newISVCDefaulter(newDefaulterFakeClient(secret)).Default(defaultCtx(admissionv1.Create, nil, false), isvc)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("exotic"))
		})

		It("returns error when referenced secret does not exist", func() {
			isvc := buildISVC(map[string]string{connectionapi.AnnotationConnections: "missing"})
			err := newISVCDefaulter(newDefaulterFakeClient()).Default(defaultCtx(admissionv1.Create, nil, false), isvc)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found"))
		})

		It("skips injection when resource is marked for deletion", func() {
			secret := testutils.BuildSecret("s", defaultNS, "s3", nil)
			isvc := buildISVC(map[string]string{connectionapi.AnnotationConnections: "s"})
			now := metav1.Now()
			isvc.DeletionTimestamp = &now
			Expect(newISVCDefaulter(newDefaulterFakeClient(secret)).Default(defaultCtx(admissionv1.Create, nil, false), isvc)).To(Succeed())
			Expect(isvc.Spec.Predictor.ServiceAccountName).To(BeEmpty())
		})

		Context("S3 connection type", func() {

			It("creates SA and injects storage key and path", func() {
				secret := testutils.BuildSecret(defaultS3Secret, defaultNS, "s3", nil)
				cli := newDefaulterFakeClient(secret)
				isvc := buildISVC(map[string]string{
					connectionapi.AnnotationConnections:    defaultS3Secret,
					connectionapi.AnnotationConnectionPath: "models/v1",
				})
				isvc.Spec.Predictor.Model = &kservev1beta1.ModelSpec{}

				Expect(newISVCDefaulter(cli).Default(defaultCtx(admissionv1.Create, nil, false), isvc)).To(Succeed())
				Expect(isvc.Spec.Predictor.ServiceAccountName).To(Equal(defaultS3SA))
				Expect(isvc.Spec.Predictor.Model.Storage).ToNot(BeNil())
				Expect(isvc.Spec.Predictor.Model.Storage.StorageKey).To(HaveValue(Equal(defaultS3Secret)))
				Expect(isvc.Spec.Predictor.Model.Storage.Path).To(HaveValue(Equal("models/v1")))
				sa := &corev1.ServiceAccount{}
				Expect(cli.Get(context.Background(), types.NamespacedName{Name: defaultS3SA, Namespace: defaultNS}, sa)).To(Succeed())
			})

			It("injects storage fields but does not create SA on dry-run", func() {
				secret := testutils.BuildSecret(defaultS3Secret, defaultNS, "s3", nil)
				cli := newDefaulterFakeClient(secret)
				isvc := buildISVC(map[string]string{
					connectionapi.AnnotationConnections:    defaultS3Secret,
					connectionapi.AnnotationConnectionPath: "models/v1",
				})
				isvc.Spec.Predictor.Model = &kservev1beta1.ModelSpec{}

				Expect(newISVCDefaulter(cli).Default(defaultCtx(admissionv1.Create, nil, true), isvc)).To(Succeed())
				Expect(isvc.Spec.Predictor.Model.Storage.StorageKey).To(HaveValue(Equal(defaultS3Secret)))
				sa := &corev1.ServiceAccount{}
				Expect(cli.Get(context.Background(), types.NamespacedName{Name: defaultS3SA, Namespace: defaultNS}, sa)).To(HaveOccurred())
			})
		})

		Context("URI connection type", func() {

			It("injects storageUri from secret https-host key", func() {
				secret := testutils.BuildSecret("uri-s", defaultNS, "uri", map[string][]byte{"https-host": []byte("https://example.com/model")})
				isvc := buildISVC(map[string]string{connectionapi.AnnotationConnections: "uri-s"})
				Expect(newISVCDefaulter(newDefaulterFakeClient(secret)).Default(defaultCtx(admissionv1.Create, nil, false), isvc)).To(Succeed())
				Expect(isvc.Spec.Predictor.Model.StorageURI).To(HaveValue(Equal("https://example.com/model")))
			})

			It("injects storageUri from secret URI key", func() {
				secret := testutils.BuildSecret("uri-s", defaultNS, "uri", map[string][]byte{"URI": []byte("s3://bucket/model")})
				isvc := buildISVC(map[string]string{connectionapi.AnnotationConnections: "uri-s"})
				Expect(newISVCDefaulter(newDefaulterFakeClient(secret)).Default(defaultCtx(admissionv1.Create, nil, false), isvc)).To(Succeed())
				Expect(isvc.Spec.Predictor.Model.StorageURI).To(HaveValue(Equal("s3://bucket/model")))
			})

			It("returns error when secret has neither URI key", func() {
				secret := testutils.BuildSecret("uri-s", defaultNS, "uri", map[string][]byte{})
				isvc := buildISVC(map[string]string{connectionapi.AnnotationConnections: "uri-s"})
				err := newISVCDefaulter(newDefaulterFakeClient(secret)).Default(defaultCtx(admissionv1.Create, nil, false), isvc)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("URI"))
			})
		})

		Context("OCI connection type", func() {

			It("injects imagePullSecrets", func() {
				secret := testutils.BuildSecret("oci-s", defaultNS, "oci", nil)
				isvc := buildISVC(map[string]string{connectionapi.AnnotationConnections: "oci-s"})
				Expect(newISVCDefaulter(newDefaulterFakeClient(secret)).Default(defaultCtx(admissionv1.Create, nil, false), isvc)).To(Succeed())
				Expect(isvc.Spec.Predictor.ImagePullSecrets).To(ConsistOf(corev1.LocalObjectReference{Name: "oci-s"}))
			})

			It("does not duplicate imagePullSecrets when already present", func() {
				secret := testutils.BuildSecret("oci-s", defaultNS, "oci", nil)
				isvc := buildISVC(map[string]string{connectionapi.AnnotationConnections: "oci-s"})
				isvc.Spec.Predictor.ImagePullSecrets = []corev1.LocalObjectReference{{Name: "oci-s"}}
				Expect(newISVCDefaulter(newDefaulterFakeClient(secret)).Default(defaultCtx(admissionv1.Create, nil, false), isvc)).To(Succeed())
				Expect(isvc.Spec.Predictor.ImagePullSecrets).To(HaveLen(1))
			})
		})
	})

	Describe("UPDATE operations", func() {

		Context("inject (annotation added)", func() {

			It("creates SA and injects S3 fields when annotation is added", func() {
				secret := testutils.BuildSecret(defaultS3Secret, defaultNS, "s3", nil)
				cli := newDefaulterFakeClient(secret)
				oldISVC := buildISVC(nil)
				newISVC := buildISVC(map[string]string{
					connectionapi.AnnotationConnections:    defaultS3Secret,
					connectionapi.AnnotationConnectionPath: "models/v1",
				})
				newISVC.Spec.Predictor.Model = &kservev1beta1.ModelSpec{}

				Expect(newISVCDefaulter(cli).Default(defaultCtx(admissionv1.Update, oldISVC, false), newISVC)).To(Succeed())
				Expect(newISVC.Spec.Predictor.ServiceAccountName).To(Equal(defaultS3SA))
				Expect(newISVC.Spec.Predictor.Model.Storage.StorageKey).To(HaveValue(Equal(defaultS3Secret)))
				Expect(newISVC.Spec.Predictor.Model.Storage.Path).To(HaveValue(Equal("models/v1")))
				sa := &corev1.ServiceAccount{}
				Expect(cli.Get(context.Background(), types.NamespacedName{Name: defaultS3SA, Namespace: defaultNS}, sa)).To(Succeed())
			})
		})

		Context("remove (annotation removed)", func() {

			It("clears SA and storage when S3 annotation is removed", func() {
				secret := testutils.BuildSecret(defaultS3Secret, defaultNS, "s3", nil)
				oldISVC := buildISVC(map[string]string{connectionapi.AnnotationConnections: defaultS3Secret})
				path, key := "models/v1", defaultS3Secret
				newISVC := buildISVC(nil)
				newISVC.Spec.Predictor.ServiceAccountName = defaultS3SA
				newISVC.Spec.Predictor.Model = &kservev1beta1.ModelSpec{}
				newISVC.Spec.Predictor.Model.Storage = &kservev1beta1.ModelStorageSpec{}
				newISVC.Spec.Predictor.Model.Storage.StorageKey = &key
				newISVC.Spec.Predictor.Model.Storage.Path = &path

				Expect(newISVCDefaulter(newDefaulterFakeClient(secret)).Default(defaultCtx(admissionv1.Update, oldISVC, false), newISVC)).To(Succeed())
				Expect(newISVC.Spec.Predictor.ServiceAccountName).To(BeEmpty())
				Expect(newISVC.Spec.Predictor.Model.Storage).To(BeNil())
			})

			It("clears storageUri when URI annotation is removed", func() {
				secret := testutils.BuildSecret("uri-s", defaultNS, "uri", nil)
				oldISVC := buildISVC(map[string]string{connectionapi.AnnotationConnections: "uri-s"})
				uri := "https://example.com"
				newISVC := buildISVC(nil)
				newISVC.Spec.Predictor.Model = &kservev1beta1.ModelSpec{}
				newISVC.Spec.Predictor.Model.StorageURI = &uri

				Expect(newISVCDefaulter(newDefaulterFakeClient(secret)).Default(defaultCtx(admissionv1.Update, oldISVC, false), newISVC)).To(Succeed())
				Expect(newISVC.Spec.Predictor.Model.StorageURI).To(BeNil())
			})

			It("removes OCI secret from imagePullSecrets when annotation is removed", func() {
				secret := testutils.BuildSecret("oci-s", defaultNS, "oci", nil)
				oldISVC := buildISVC(map[string]string{connectionapi.AnnotationConnections: "oci-s"})
				newISVC := buildISVC(nil)
				newISVC.Spec.Predictor.ImagePullSecrets = []corev1.LocalObjectReference{{Name: "oci-s"}}

				Expect(newISVCDefaulter(newDefaulterFakeClient(secret)).Default(defaultCtx(admissionv1.Update, oldISVC, false), newISVC)).To(Succeed())
				Expect(newISVC.Spec.Predictor.ImagePullSecrets).To(BeEmpty())
			})

			It("performs full cleanup when old secret has been deleted", func() {
				// deleted-secret is intentionally absent from the fake client
				oldISVC := buildISVC(map[string]string{connectionapi.AnnotationConnections: "deleted-secret"})
				uri := "https://example.com"
				newISVC := buildISVC(nil)
				newISVC.Spec.Predictor.ServiceAccountName = "deleted-secret-sa"
				newISVC.Spec.Predictor.Model = &kservev1beta1.ModelSpec{}
				newISVC.Spec.Predictor.Model.StorageURI = &uri
				newISVC.Spec.Predictor.Model.Storage = &kservev1beta1.ModelStorageSpec{}
				newISVC.Spec.Predictor.ImagePullSecrets = []corev1.LocalObjectReference{{Name: "deleted-secret"}}

				Expect(newISVCDefaulter(newDefaulterFakeClient()).Default(defaultCtx(admissionv1.Update, oldISVC, false), newISVC)).To(Succeed())
				Expect(newISVC.Spec.Predictor.ServiceAccountName).To(BeEmpty())
				Expect(newISVC.Spec.Predictor.Model.StorageURI).To(BeNil())
				Expect(newISVC.Spec.Predictor.Model.Storage).To(BeNil())
				Expect(newISVC.Spec.Predictor.ImagePullSecrets).To(BeEmpty())
			})

			It("preserves user-set SA name when removing S3 connection", func() {
				secret := testutils.BuildSecret(defaultS3Secret, defaultNS, "s3", nil)
				oldISVC := buildISVC(map[string]string{connectionapi.AnnotationConnections: defaultS3Secret})
				newISVC := buildISVC(nil)
				newISVC.Spec.Predictor.ServiceAccountName = "user-custom-sa"

				Expect(newISVCDefaulter(newDefaulterFakeClient(secret)).Default(defaultCtx(admissionv1.Update, oldISVC, false), newISVC)).To(Succeed())
				Expect(newISVC.Spec.Predictor.ServiceAccountName).To(Equal("user-custom-sa"))
			})
		})

		Context("replace (type or secret changed)", func() {

			It("cleans up S3 and injects URI when connection type changes", func() {
				s3Secret := testutils.BuildSecret(defaultS3Secret, defaultNS, "s3", nil)
				uriSecret := testutils.BuildSecret("uri-s", defaultNS, "uri", map[string][]byte{"URI": []byte("https://x")})
				cli := newDefaulterFakeClient(s3Secret, uriSecret)
				oldISVC := buildISVC(map[string]string{connectionapi.AnnotationConnections: defaultS3Secret})
				key := defaultS3Secret
				newISVC := buildISVC(map[string]string{connectionapi.AnnotationConnections: "uri-s"})
				newISVC.Spec.Predictor.ServiceAccountName = defaultS3SA
				newISVC.Spec.Predictor.Model = &kservev1beta1.ModelSpec{}
				newISVC.Spec.Predictor.Model.Storage = &kservev1beta1.ModelStorageSpec{}
				newISVC.Spec.Predictor.Model.Storage.StorageKey = &key

				Expect(newISVCDefaulter(cli).Default(defaultCtx(admissionv1.Update, oldISVC, false), newISVC)).To(Succeed())
				Expect(newISVC.Spec.Predictor.ServiceAccountName).To(BeEmpty())
				Expect(newISVC.Spec.Predictor.Model.Storage).To(BeNil())
				Expect(newISVC.Spec.Predictor.Model.StorageURI).To(HaveValue(Equal("https://x")))
			})

			It("preserves storageUri on OCI Replace when old secret is deleted", func() {
				// Old OCI secret is gone, but the injected-connection-type annotation
				// on the old ISVC lets GetOldConnectionInfo recover the type.
				ociSecret := testutils.BuildSecret("new-oci", defaultNS, "oci", nil)
				oldISVC := buildISVC(map[string]string{
					connectionapi.AnnotationConnections:            "deleted-oci",
					connectionapi.AnnotationInjectedConnectionType: "oci",
				})
				uri := "oci://registry.example.com/model:latest"
				newISVC := buildISVC(map[string]string{connectionapi.AnnotationConnections: "new-oci"})
				newISVC.Spec.Predictor.Model = &kservev1beta1.ModelSpec{}
				newISVC.Spec.Predictor.Model.StorageURI = &uri
				newISVC.Spec.Predictor.ImagePullSecrets = []corev1.LocalObjectReference{{Name: "deleted-oci"}}

				Expect(newISVCDefaulter(newDefaulterFakeClient(ociSecret)).Default(defaultCtx(admissionv1.Update, oldISVC, false), newISVC)).To(Succeed())
				// storageUri must survive — OCI cleanup only touches imagePullSecrets
				Expect(newISVC.Spec.Predictor.Model.StorageURI).To(HaveValue(Equal(uri)))
				// imagePullSecrets should be cleaned (old) and re-injected (new)
				Expect(newISVC.Spec.Predictor.ImagePullSecrets).To(ConsistOf(corev1.LocalObjectReference{Name: "new-oci"}))
			})

			It("re-injects SA and storage on S3 Replace when old secret is deleted", func() {
				// Old S3 secret is gone, annotation recovers the type.
				s3Secret := testutils.BuildSecret(defaultS3Secret, defaultNS, "s3", nil)
				cli := newDefaulterFakeClient(s3Secret)
				oldISVC := buildISVC(map[string]string{
					connectionapi.AnnotationConnections:            "deleted-s3",
					connectionapi.AnnotationInjectedConnectionType: "s3",
				})
				uri := "oci://registry.example.com/model:latest"
				newISVC := buildISVC(map[string]string{
					connectionapi.AnnotationConnections:    defaultS3Secret,
					connectionapi.AnnotationConnectionPath: "models/v1",
				})
				newISVC.Spec.Predictor.ServiceAccountName = "deleted-s3-sa"
				newISVC.Spec.Predictor.Model = &kservev1beta1.ModelSpec{}
				newISVC.Spec.Predictor.Model.StorageURI = &uri

				Expect(newISVCDefaulter(cli).Default(defaultCtx(admissionv1.Update, oldISVC, false), newISVC)).To(Succeed())
				// S3 fields should be re-injected
				Expect(newISVC.Spec.Predictor.ServiceAccountName).To(Equal(defaultS3SA))
				Expect(newISVC.Spec.Predictor.Model.Storage).ToNot(BeNil())
				Expect(newISVC.Spec.Predictor.Model.Storage.StorageKey).To(HaveValue(Equal(defaultS3Secret)))
				// storageUri should survive — S3 cleanup doesn't touch it
				Expect(newISVC.Spec.Predictor.Model.StorageURI).To(HaveValue(Equal(uri)))
			})

			It("re-injects URI on Replace when old secret is deleted", func() {
				// Old URI secret is gone, annotation recovers the type.
				uriSecret := testutils.BuildSecret("new-uri", defaultNS, "uri", map[string][]byte{"URI": []byte("https://new.example.com")})
				oldISVC := buildISVC(map[string]string{
					connectionapi.AnnotationConnections:            "deleted-uri",
					connectionapi.AnnotationInjectedConnectionType: "uri",
				})
				key := defaultS3Secret
				newISVC := buildISVC(map[string]string{connectionapi.AnnotationConnections: "new-uri"})
				newISVC.Spec.Predictor.Model = &kservev1beta1.ModelSpec{}
				newISVC.Spec.Predictor.Model.Storage = &kservev1beta1.ModelStorageSpec{}
				newISVC.Spec.Predictor.Model.Storage.StorageKey = &key
				newISVC.Spec.Predictor.ImagePullSecrets = []corev1.LocalObjectReference{{Name: "user-pull-secret"}}

				Expect(newISVCDefaulter(newDefaulterFakeClient(uriSecret)).Default(defaultCtx(admissionv1.Update, oldISVC, false), newISVC)).To(Succeed())
				// URI field should be re-injected
				Expect(newISVC.Spec.Predictor.Model.StorageURI).To(HaveValue(Equal("https://new.example.com")))
				// storage and imagePullSecrets should survive — URI cleanup only touches storageUri
				Expect(newISVC.Spec.Predictor.Model.Storage).ToNot(BeNil())
				Expect(newISVC.Spec.Predictor.ImagePullSecrets).To(ConsistOf(corev1.LocalObjectReference{Name: "user-pull-secret"}))
			})

			It("replaces S3 fields when connection path changes", func() {
				secret := testutils.BuildSecret(defaultS3Secret, defaultNS, "s3", nil)
				oldISVC := buildISVC(map[string]string{
					connectionapi.AnnotationConnections:    defaultS3Secret,
					connectionapi.AnnotationConnectionPath: "old-path",
				})
				newISVC := buildISVC(map[string]string{
					connectionapi.AnnotationConnections:    defaultS3Secret,
					connectionapi.AnnotationConnectionPath: "new-path",
				})

				Expect(newISVCDefaulter(newDefaulterFakeClient(secret)).Default(defaultCtx(admissionv1.Update, oldISVC, false), newISVC)).To(Succeed())
				Expect(newISVC.Spec.Predictor.Model.Storage.Path).To(HaveValue(Equal("new-path")))
			})
		})

		Context("none (no change)", func() {

			It("produces no mutation when neither old nor new has annotation", func() {
				oldISVC := buildISVC(nil)
				newISVC := buildISVC(nil)
				Expect(newISVCDefaulter(newDefaulterFakeClient()).Default(defaultCtx(admissionv1.Update, oldISVC, false), newISVC)).To(Succeed())
				Expect(newISVC.Spec.Predictor.Model).To(BeNil())
			})

			It("produces no mutation for identical S3 connection", func() {
				secret := testutils.BuildSecret(defaultS3Secret, defaultNS, "s3", nil)
				annots := map[string]string{
					connectionapi.AnnotationConnections:    defaultS3Secret,
					connectionapi.AnnotationConnectionPath: "models/v1",
				}
				path, key := "models/v1", defaultS3Secret
				oldISVC := buildISVC(annots)
				oldISVC.Spec.Predictor.ServiceAccountName = defaultS3SA
				oldISVC.Spec.Predictor.Model = &kservev1beta1.ModelSpec{}
				oldISVC.Spec.Predictor.Model.Storage = &kservev1beta1.ModelStorageSpec{}
				oldISVC.Spec.Predictor.Model.Storage.StorageKey = &key
				oldISVC.Spec.Predictor.Model.Storage.Path = &path
				newISVC := buildISVC(annots)
				newISVC.Spec.Predictor.ServiceAccountName = defaultS3SA
				newISVC.Spec.Predictor.Model = &kservev1beta1.ModelSpec{}
				newISVC.Spec.Predictor.Model.Storage = &kservev1beta1.ModelStorageSpec{}
				newISVC.Spec.Predictor.Model.Storage.StorageKey = &key
				newISVC.Spec.Predictor.Model.Storage.Path = &path

				Expect(newISVCDefaulter(newDefaulterFakeClient(secret)).Default(defaultCtx(admissionv1.Update, oldISVC, false), newISVC)).To(Succeed())
				Expect(newISVC.Spec.Predictor.ServiceAccountName).To(Equal(defaultS3SA))
				Expect(newISVC.Spec.Predictor.Model.Storage.Path).To(HaveValue(Equal("models/v1")))
			})

			It("does not trigger replacement when URI path annotation changes", func() {
				secret := testutils.BuildSecret("uri-s", defaultNS, "uri", map[string][]byte{"URI": []byte("https://x")})
				uri := "https://x"
				oldISVC := buildISVC(map[string]string{
					connectionapi.AnnotationConnections:    "uri-s",
					connectionapi.AnnotationConnectionPath: "old",
				})
				oldISVC.Spec.Predictor.Model = &kservev1beta1.ModelSpec{}
				oldISVC.Spec.Predictor.Model.StorageURI = &uri
				newISVC := buildISVC(map[string]string{
					connectionapi.AnnotationConnections:    "uri-s",
					connectionapi.AnnotationConnectionPath: "new",
				})
				newISVC.Spec.Predictor.Model = &kservev1beta1.ModelSpec{}
				newISVC.Spec.Predictor.Model.StorageURI = &uri

				Expect(newISVCDefaulter(newDefaulterFakeClient(secret)).Default(defaultCtx(admissionv1.Update, oldISVC, false), newISVC)).To(Succeed())
				Expect(newISVC.Spec.Predictor.Model.StorageURI).To(HaveValue(Equal("https://x")))
			})
		})
	})

	Describe("S3 path priority", func() {

		It("prefers user-set path over annotation path", func() {
			secret := testutils.BuildSecret(defaultS3Secret, defaultNS, "s3", nil)
			isvc := buildISVC(map[string]string{
				connectionapi.AnnotationConnections:    defaultS3Secret,
				connectionapi.AnnotationConnectionPath: "ann-path",
			})
			userPath := "user-path"
			isvc.Spec.Predictor.Model = &kservev1beta1.ModelSpec{}
			isvc.Spec.Predictor.Model.Storage = &kservev1beta1.ModelStorageSpec{}
			isvc.Spec.Predictor.Model.Storage.Path = &userPath

			Expect(newISVCDefaulter(newDefaulterFakeClient(secret)).Default(defaultCtx(admissionv1.Create, nil, false), isvc)).To(Succeed())
			Expect(isvc.Spec.Predictor.Model.Storage.Path).To(HaveValue(Equal("user-path")))
		})

		It("uses annotation path over old spec path on UPDATE replace", func() {
			secret := testutils.BuildSecret(defaultS3Secret, defaultNS, "s3", nil)
			// Same secret but different path annotation forces replace action
			oldISVC := buildISVC(map[string]string{
				connectionapi.AnnotationConnections:    defaultS3Secret,
				connectionapi.AnnotationConnectionPath: "different-old-path",
			})
			newISVC := buildISVC(map[string]string{
				connectionapi.AnnotationConnections:    defaultS3Secret,
				connectionapi.AnnotationConnectionPath: "ann-path",
			})

			Expect(newISVCDefaulter(newDefaulterFakeClient(secret)).Default(defaultCtx(admissionv1.Update, oldISVC, false), newISVC)).To(Succeed())
			Expect(newISVC.Spec.Predictor.Model.Storage.Path).To(HaveValue(Equal("ann-path")))
		})

		It("falls back to old spec path when no annotation is set on UPDATE inject", func() {
			secret := testutils.BuildSecret(defaultS3Secret, defaultNS, "s3", nil)
			// Old ISVC has no connection annotation (inject action on update)
			oldPath := "old-spec-path"
			oldISVC := buildISVC(nil)
			oldISVC.Spec.Predictor.Model = &kservev1beta1.ModelSpec{}
			oldISVC.Spec.Predictor.Model.Storage = &kservev1beta1.ModelStorageSpec{}
			oldISVC.Spec.Predictor.Model.Storage.Path = &oldPath
			// New ISVC adds connection but no path annotation
			newISVC := buildISVC(map[string]string{
				connectionapi.AnnotationConnections: defaultS3Secret,
			})

			Expect(newISVCDefaulter(newDefaulterFakeClient(secret)).Default(defaultCtx(admissionv1.Update, oldISVC, false), newISVC)).To(Succeed())
			Expect(newISVC.Spec.Predictor.Model.Storage.Path).To(HaveValue(Equal("old-spec-path")))
		})
	})

	Describe("Backward compatibility (deprecated connection-type-ref)", func() {

		It("injects S3 fields using connection-type-ref: s3", func() {
			secret := testutils.BuildSecretWithRef(defaultS3Secret, defaultNS, "s3", nil)
			isvc := buildISVC(map[string]string{
				connectionapi.AnnotationConnections:    defaultS3Secret,
				connectionapi.AnnotationConnectionPath: "models/v1",
			})
			Expect(newISVCDefaulter(newDefaulterFakeClient(secret)).Default(defaultCtx(admissionv1.Create, nil, false), isvc)).To(Succeed())
			Expect(isvc.Spec.Predictor.ServiceAccountName).To(Equal(defaultS3SA))
		})

		It("injects storageUri using connection-type-ref: uri-v1", func() {
			secret := testutils.BuildSecretWithRef("uri-s", defaultNS, "uri-v1", map[string][]byte{"URI": []byte("https://x")})
			isvc := buildISVC(map[string]string{connectionapi.AnnotationConnections: "uri-s"})
			Expect(newISVCDefaulter(newDefaulterFakeClient(secret)).Default(defaultCtx(admissionv1.Create, nil, false), isvc)).To(Succeed())
			Expect(isvc.Spec.Predictor.Model.StorageURI).To(HaveValue(Equal("https://x")))
		})

		It("injects imagePullSecrets using connection-type-ref: oci-v1", func() {
			secret := testutils.BuildSecretWithRef("oci-s", defaultNS, "oci-v1", nil)
			isvc := buildISVC(map[string]string{connectionapi.AnnotationConnections: "oci-s"})
			Expect(newISVCDefaulter(newDefaulterFakeClient(secret)).Default(defaultCtx(admissionv1.Create, nil, false), isvc)).To(Succeed())
			Expect(isvc.Spec.Predictor.ImagePullSecrets).To(ConsistOf(corev1.LocalObjectReference{Name: "oci-s"}))
		})
	})

	Describe("Utility edge cases exercised through the webhook", func() {

		It("does not overwrite user-set SA name on S3 CREATE", func() {
			secret := testutils.BuildSecret(defaultS3Secret, defaultNS, "s3", nil)
			isvc := buildISVC(map[string]string{
				connectionapi.AnnotationConnections:    defaultS3Secret,
				connectionapi.AnnotationConnectionPath: "models/v1",
			})
			isvc.Spec.Predictor.ServiceAccountName = "user-custom-sa"
			isvc.Spec.Predictor.Model = &kservev1beta1.ModelSpec{}

			Expect(newISVCDefaulter(newDefaulterFakeClient(secret)).Default(defaultCtx(admissionv1.Create, nil, false), isvc)).To(Succeed())
			Expect(isvc.Spec.Predictor.ServiceAccountName).To(Equal("user-custom-sa"))
			Expect(isvc.Spec.Predictor.Model.Storage.StorageKey).To(HaveValue(Equal(defaultS3Secret)))
		})

		It("merges OCI imagePullSecrets with pre-existing different entries", func() {
			secret := testutils.BuildSecret("oci-s", defaultNS, "oci", nil)
			isvc := buildISVC(map[string]string{connectionapi.AnnotationConnections: "oci-s"})
			isvc.Spec.Predictor.ImagePullSecrets = []corev1.LocalObjectReference{{Name: "existing"}}

			Expect(newISVCDefaulter(newDefaulterFakeClient(secret)).Default(defaultCtx(admissionv1.Create, nil, false), isvc)).To(Succeed())
			Expect(isvc.Spec.Predictor.ImagePullSecrets).To(ConsistOf(
				corev1.LocalObjectReference{Name: "existing"},
				corev1.LocalObjectReference{Name: "oci-s"},
			))
		})

		It("preserves other imagePullSecrets entries on OCI removal", func() {
			secret := testutils.BuildSecret("oci-s", defaultNS, "oci", nil)
			oldISVC := buildISVC(map[string]string{connectionapi.AnnotationConnections: "oci-s"})
			newISVC := buildISVC(nil)
			newISVC.Spec.Predictor.ImagePullSecrets = []corev1.LocalObjectReference{{Name: "other"}, {Name: "oci-s"}}

			Expect(newISVCDefaulter(newDefaulterFakeClient(secret)).Default(defaultCtx(admissionv1.Update, oldISVC, false), newISVC)).To(Succeed())
			Expect(newISVC.Spec.Predictor.ImagePullSecrets).To(ConsistOf(corev1.LocalObjectReference{Name: "other"}))
		})

		It("triggers replacement when same S3 type but different secret name", func() {
			oldSec := testutils.BuildSecret("old-secret", defaultNS, "s3", nil)
			newSec := testutils.BuildSecret(defaultS3Secret, defaultNS, "s3", nil)
			oldISVC := buildISVC(map[string]string{
				connectionapi.AnnotationConnections:    "old-secret",
				connectionapi.AnnotationConnectionPath: "models/v1",
			})
			newISVC := buildISVC(map[string]string{
				connectionapi.AnnotationConnections:    defaultS3Secret,
				connectionapi.AnnotationConnectionPath: "models/v1",
			})
			newISVC.Spec.Predictor.ServiceAccountName = "old-secret-sa"

			Expect(newISVCDefaulter(newDefaulterFakeClient(oldSec, newSec)).Default(defaultCtx(admissionv1.Update, oldISVC, false), newISVC)).To(Succeed())
			Expect(newISVC.Spec.Predictor.ServiceAccountName).To(Equal(defaultS3SA))
			Expect(newISVC.Spec.Predictor.Model.Storage.StorageKey).To(HaveValue(Equal(defaultS3Secret)))
		})

		It("prefers https-host over URI key when both present", func() {
			secret := testutils.BuildSecret("uri-s", defaultNS, "uri", map[string][]byte{
				"https-host": []byte("https://preferred.com"),
				"URI":        []byte("https://fallback.com"),
			})
			isvc := buildISVC(map[string]string{connectionapi.AnnotationConnections: "uri-s"})
			Expect(newISVCDefaulter(newDefaulterFakeClient(secret)).Default(defaultCtx(admissionv1.Create, nil, false), isvc)).To(Succeed())
			Expect(isvc.Spec.Predictor.Model.StorageURI).To(HaveValue(Equal("https://preferred.com")))
		})

		It("filters imagePullSecrets by name on unknown type removal", func() {
			// deleted-secret absent from cluster; GetOldConnectionInfo returns Type=""
			oldISVC := buildISVC(map[string]string{connectionapi.AnnotationConnections: "deleted-secret"})
			newISVC := buildISVC(nil)
			newISVC.Spec.Predictor.ImagePullSecrets = []corev1.LocalObjectReference{{Name: "a"}, {Name: "b"}}

			Expect(newISVCDefaulter(newDefaulterFakeClient()).Default(defaultCtx(admissionv1.Update, oldISVC, false), newISVC)).To(Succeed())
			// CleanupOCIImagePullSecrets("deleted-secret") is a no-op since neither "a" nor "b" match
			Expect(newISVC.Spec.Predictor.ImagePullSecrets).To(ConsistOf(
				corev1.LocalObjectReference{Name: "a"},
				corev1.LocalObjectReference{Name: "b"},
			))
		})

		It("clears entire imagePullSecrets when old SecretName is empty", func() {
			// Corner case unreachable through the normal webhook flow: DetermineAction
			// requires oldConn.SecretName != "" to produce Remove. Call performISVCCleanup
			// directly since it is in the same package.
			isvc := buildISVC(nil)
			isvc.Spec.Predictor.ImagePullSecrets = []corev1.LocalObjectReference{{Name: "a"}}
			Expect(performISVCCleanup(isvc, connectionapi.ConnectionInfo{SecretName: "", Type: ""})).To(Succeed())
			Expect(isvc.Spec.Predictor.ImagePullSecrets).To(BeNil())
		})

		It("prefers protocol annotation over deprecated ref on Secret", func() {
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "s", Namespace: defaultNS,
					Annotations: map[string]string{
						connectionapi.AnnotationConnectionTypeProtocol: "uri",
						connectionapi.AnnotationConnectionTypeRef:      "oci-v1",
					},
				},
				Data: map[string][]byte{"URI": []byte("https://x")},
			}
			isvc := buildISVC(map[string]string{connectionapi.AnnotationConnections: "s"})
			Expect(newISVCDefaulter(newDefaulterFakeClient(secret)).Default(defaultCtx(admissionv1.Create, nil, false), isvc)).To(Succeed())
			Expect(isvc.Spec.Predictor.Model.StorageURI).To(HaveValue(Equal("https://x")))
			Expect(isvc.Spec.Predictor.ImagePullSecrets).To(BeEmpty())
		})

		It("succeeds when SA already exists (idempotent creation)", func() {
			secret := testutils.BuildSecret(defaultS3Secret, defaultNS, "s3", nil)
			existingSA := &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{Name: defaultS3SA, Namespace: defaultNS},
			}
			isvc := buildISVC(map[string]string{
				connectionapi.AnnotationConnections:    defaultS3Secret,
				connectionapi.AnnotationConnectionPath: "models/v1",
			})
			isvc.Spec.Predictor.Model = &kservev1beta1.ModelSpec{}
			Expect(newISVCDefaulter(newDefaulterFakeClient(secret, existingSA)).Default(defaultCtx(admissionv1.Create, nil, false), isvc)).To(Succeed())
			Expect(isvc.Spec.Predictor.ServiceAccountName).To(Equal(defaultS3SA))
		})
	})

	Describe("Injected connection type annotation lifecycle", func() {

		It("sets the annotation on CREATE injection", func() {
			secret := testutils.BuildSecret(defaultS3Secret, defaultNS, "s3", nil)
			isvc := buildISVC(map[string]string{
				connectionapi.AnnotationConnections:    defaultS3Secret,
				connectionapi.AnnotationConnectionPath: "models/v1",
			})
			isvc.Spec.Predictor.Model = &kservev1beta1.ModelSpec{}

			Expect(newISVCDefaulter(newDefaulterFakeClient(secret)).Default(defaultCtx(admissionv1.Create, nil, false), isvc)).To(Succeed())
			Expect(isvc.Annotations).To(HaveKeyWithValue(connectionapi.AnnotationInjectedConnectionType, "s3"))
		})

		It("sets the annotation for OCI injection", func() {
			secret := testutils.BuildSecret("oci-s", defaultNS, "oci", nil)
			isvc := buildISVC(map[string]string{connectionapi.AnnotationConnections: "oci-s"})

			Expect(newISVCDefaulter(newDefaulterFakeClient(secret)).Default(defaultCtx(admissionv1.Create, nil, false), isvc)).To(Succeed())
			Expect(isvc.Annotations).To(HaveKeyWithValue(connectionapi.AnnotationInjectedConnectionType, "oci"))
		})

		It("removes the annotation on connection Remove", func() {
			secret := testutils.BuildSecret(defaultS3Secret, defaultNS, "s3", nil)
			oldISVC := buildISVC(map[string]string{
				connectionapi.AnnotationConnections:            defaultS3Secret,
				connectionapi.AnnotationInjectedConnectionType: "s3",
			})
			newISVC := buildISVC(nil)
			newISVC.Annotations = map[string]string{
				connectionapi.AnnotationInjectedConnectionType: "s3",
			}
			newISVC.Spec.Predictor.ServiceAccountName = defaultS3SA

			Expect(newISVCDefaulter(newDefaulterFakeClient(secret)).Default(defaultCtx(admissionv1.Update, oldISVC, false), newISVC)).To(Succeed())
			Expect(newISVC.Annotations).ToNot(HaveKey(connectionapi.AnnotationInjectedConnectionType))
		})

		It("updates the annotation on connection Replace with different type", func() {
			s3Secret := testutils.BuildSecret(defaultS3Secret, defaultNS, "s3", nil)
			uriSecret := testutils.BuildSecret("uri-s", defaultNS, "uri", map[string][]byte{"URI": []byte("https://x")})
			cli := newDefaulterFakeClient(s3Secret, uriSecret)
			oldISVC := buildISVC(map[string]string{
				connectionapi.AnnotationConnections:            defaultS3Secret,
				connectionapi.AnnotationInjectedConnectionType: "s3",
			})
			newISVC := buildISVC(map[string]string{connectionapi.AnnotationConnections: "uri-s"})
			newISVC.Annotations[connectionapi.AnnotationInjectedConnectionType] = "s3"
			newISVC.Spec.Predictor.ServiceAccountName = defaultS3SA

			Expect(newISVCDefaulter(cli).Default(defaultCtx(admissionv1.Update, oldISVC, false), newISVC)).To(Succeed())
			Expect(newISVC.Annotations).To(HaveKeyWithValue(connectionapi.AnnotationInjectedConnectionType, "uri"))
		})
	})
})
