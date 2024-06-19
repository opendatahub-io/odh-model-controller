package webhook

import (
	"context"
	"fmt"

	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// +kubebuilder:webhook:admissionReviewVersions=v1,path=/mutate-serving-kserve-service,mutating=true,failurePolicy=fail,groups="",resources=services,verbs=create,versions=v1,name=mutating.kserve-service.odh-model-controller.opendatahub.io,sideEffects=None

type kserveServiceMutator struct {
	client  client.Client
	Decoder *admission.Decoder
}

func NewKserveServiceMutator(client client.Client) *kserveServiceMutator {
	return &kserveServiceMutator{client: client}
}

// Default implements the admission.Defaulter interface to mutate the resources
func (m *kserveServiceMutator) Default(ctx context.Context, obj runtime.Object) error {
	log := logf.FromContext(ctx).WithName("KserviceServiceMutateWebhook")

	svc, ok := obj.(*corev1.Service)
	if !ok {
		return fmt.Errorf("unexpected object type")
	}

	// Retrieve the InferenceService directly by name
	var isvc kservev1beta1.InferenceService
	err := m.client.Get(ctx, client.ObjectKey{Name: svc.Name, Namespace: svc.Namespace}, &isvc)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// If InferenceService is not found, return without error
			return nil
		}
		return fmt.Errorf("failed to get InferenceService: %w", err)
	}

	// Add the annotation if a matching InferenceService is found
	if svc.Annotations == nil {
		svc.Annotations = make(map[string]string)
	}
	svc.Annotations["service.beta.openshift.io/serving-cert-secret-name"] = isvc.Name
	log.Info("Added annotation to Service", "ServiceName", svc.Name, "Annotation", svc.Annotations["service.beta.openshift.io/serving-cert-secret-name"])

	return nil
}

// InjectDecoder injects the decoder into the KserveServiceMutator
func (m *kserveServiceMutator) InjectDecoder(d *admission.Decoder) error {
	m.Decoder = d
	return nil
}
