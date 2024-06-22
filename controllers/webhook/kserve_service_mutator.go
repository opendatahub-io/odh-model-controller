package webhook

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
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
	log.Info("AAAAAAAAAAAAAAAAAAA - mutatingwebhook Called")

	svc, ok := obj.(*corev1.Service)
	if !ok {
		return fmt.Errorf("unexpected object type")
	}

	log.Info("AAAAAAAAAAAAAAAAAAA1 - before checking")
	if !hasInferenceServiceOwner(svc) {
		return nil
	}
	log.Info("AAAAAAAAAAAAAAAAAAA1 - after checking")

	// Add the annotation if a matching InferenceService is found
	if svc.Annotations == nil {
		svc.Annotations = make(map[string]string)
	}
	svc.Annotations["service.beta.openshift.io/serving-cert-secret-name"] = svc.Name
	log.Info("Added annotation to Service", "ServiceName", svc.Name, "Annotation", svc.Annotations["service.beta.openshift.io/serving-cert-secret-name"])

	return nil
}

// InjectDecoder injects the decoder into the KserveServiceMutator
func (m *kserveServiceMutator) InjectDecoder(d *admission.Decoder) error {
	m.Decoder = d
	return nil
}

// check if the src secret has ownerReferences for InferenceService
func hasInferenceServiceOwner(obj client.Object) bool {
	ownerReferences := obj.GetOwnerReferences()
	for _, ownerReference := range ownerReferences {
		if ownerReference.Kind == "InferenceService" {
			return true
		}
	}
	return false
}
