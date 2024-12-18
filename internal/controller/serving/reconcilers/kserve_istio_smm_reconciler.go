package reconcilers

import (
	"context"
	"errors"

	"github.com/go-logr/logr"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "maistra.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/comparators"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/processors"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/resources"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"
)

type KserveServiceMeshMemberReconciler struct {
	SingleResourcePerNamespace
	client         client.Client
	smmHandler     resources.ServiceMeshMemberHandler
	deltaProcessor processors.DeltaProcessor
}

func NewKserveServiceMeshMemberReconciler(client client.Client) *KserveServiceMeshMemberReconciler {
	return &KserveServiceMeshMemberReconciler{
		client:         client,
		smmHandler:     resources.NewServiceMeshMember(client),
		deltaProcessor: processors.NewDeltaProcessor(),
	}
}

func (r *KserveServiceMeshMemberReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	log.V(1).Info("Verifying that the namespace is enrolled to the mesh")

	// Create Desired resource
	desiredResource := r.createDesiredResource(ctx, isvc)

	// Get Existing resource
	existingResource, err := r.getExistingResource(ctx, log, isvc.Namespace)
	if err != nil {
		return err
	}

	// Process Delta
	if err = r.processDelta(ctx, log, desiredResource, existingResource); err != nil {
		return err
	}
	return nil
}

func (r *KserveServiceMeshMemberReconciler) Cleanup(ctx context.Context, log logr.Logger, namespace string) error {
	existingSMM, getError := r.getExistingResource(ctx, log, namespace)
	if getError != nil {
		return getError
	}

	return r.processDelta(ctx, log, nil, existingSMM)
}

func (r *KserveServiceMeshMemberReconciler) createDesiredResource(ctx context.Context, isvc *kservev1beta1.InferenceService) *v1.ServiceMeshMember {
	smcpName, smcpNamespace := utils.GetIstioControlPlaneName(ctx, r.client)

	return &v1.ServiceMeshMember{
		ObjectMeta: metav1.ObjectMeta{
			Name:      constants.ServiceMeshMemberName,
			Namespace: isvc.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       "odh-model-controller",
				"app.kubernetes.io/component":  "kserve",
				"app.kubernetes.io/part-of":    "odh-model-serving",
				"app.kubernetes.io/managed-by": "odh-model-controller",
			},
			Annotations: nil,
		},
		Spec: v1.ServiceMeshMemberSpec{
			ControlPlaneRef: v1.ServiceMeshControlPlaneRef{
				Name:      smcpName,
				Namespace: smcpNamespace,
			},
		},
	}
}

func (r *KserveServiceMeshMemberReconciler) getExistingResource(ctx context.Context, log logr.Logger, namespace string) (*v1.ServiceMeshMember, error) {
	return r.smmHandler.Fetch(ctx, log, namespace, constants.ServiceMeshMemberName)
}

func (r *KserveServiceMeshMemberReconciler) processDelta(ctx context.Context, log logr.Logger, desiredSMM *v1.ServiceMeshMember, existingSMM *v1.ServiceMeshMember) error {
	comparator := comparators.GetServiceMeshMemberComparator()
	delta := r.deltaProcessor.ComputeDelta(comparator, desiredSMM, existingSMM)

	if !delta.HasChanges() {
		log.V(1).Info("No delta found")
		return nil
	}

	if delta.IsAdded() {
		log.V(1).Info("Delta found", "create", desiredSMM.GetName())
		if err := r.client.Create(ctx, desiredSMM); err != nil {
			return err
		}
	}

	if delta.IsUpdated() {
		// Don't update a resource that we don't own.
		if existingSMM.Labels["app.kubernetes.io/managed-by"] != "odh-model-controller" {
			log.Error(errors.New("non suitable ServiceMeshMember resource"),
				"There is a user-owned ServiceMeshMember resource that needs to be updated. Inference Services may not work properly.",
				"smm.name", existingSMM.GetName(),
				"smm.desired.smcp_namespace", desiredSMM.Spec.ControlPlaneRef.Namespace,
				"smm.desired.smcp_name", desiredSMM.Spec.ControlPlaneRef.Name,
				"smm.current.smcp_namespace", existingSMM.Spec.ControlPlaneRef.Namespace,
				"smm.current.smcp_name", existingSMM.Spec.ControlPlaneRef.Name)
			// Don't return error because it is not recoverable. It does not make sense
			// to keep trying. It needs user intervention.
			return nil
		}

		log.V(1).Info("Delta found", "update", existingSMM.GetName())
		rp := existingSMM.DeepCopy()
		rp.Spec = desiredSMM.Spec

		if err := r.client.Update(ctx, rp); err != nil {
			return err
		}
	}

	if delta.IsRemoved() {
		// Don't delete a resource that we don't own.
		if existingSMM.Labels["app.kubernetes.io/managed-by"] != "odh-model-controller" {
			log.Info("Model Serving no longer needs the namespace enrolled to the mesh. The ServiceMeshMember resource is not removed, because it is user-owned.")
			return nil
		}

		log.V(1).Info("Delta found", "delete", existingSMM.GetName())
		if err := r.client.Delete(ctx, existingSMM); err != nil {
			return err
		}
	}
	return nil
}
