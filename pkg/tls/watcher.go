package tls

import (
	"context"
	"reflect"

	configv1 "github.com/openshift/api/config/v1"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ctrl "sigs.k8s.io/controller-runtime"
)

const apiServerName = "cluster"

// ProfileWatcher watches the APIServer CR and triggers a callback when the
// TLS security profile changes. Modeled after the CRC SecurityProfileWatcher
// but without the CRC dependency (KEDA version pin blocks CRC adoption).
type ProfileWatcher struct {
	client.Client
	InitialProfileSpec configv1.TLSProfileSpec
	OnProfileChange    func(ctx context.Context, old, new configv1.TLSProfileSpec)

	lastProfile configv1.TLSProfileSpec
}

func (w *ProfileWatcher) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	if req.Name != apiServerName {
		return reconcile.Result{}, nil
	}

	apiServer := &configv1.APIServer{}
	if err := w.Get(ctx, req.NamespacedName, apiServer); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	currentProfile := *resolveProfileSpec(apiServer.Spec.TLSSecurityProfile)
	if w.OnProfileChange != nil && !reflect.DeepEqual(w.lastProfile, currentProfile) {
		old := w.lastProfile
		w.lastProfile = currentProfile
		w.OnProfileChange(ctx, old, currentProfile)
	}

	return reconcile.Result{}, nil
}

func (w *ProfileWatcher) SetupWithManager(mgr ctrl.Manager) error {
	w.lastProfile = w.InitialProfileSpec

	return ctrl.NewControllerManagedBy(mgr).
		Named("tls-profile-watcher").
		WithOptions(controller.Options{NeedLeaderElection: boolPtr(false)}).
		For(&configv1.APIServer{}, builder.WithPredicates(predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				return e.Object.GetName() == apiServerName
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				return e.ObjectNew.GetName() == apiServerName
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				return e.Object.GetName() == apiServerName
			},
			GenericFunc: func(e event.GenericEvent) bool {
				return e.Object.GetName() == apiServerName
			},
		})).
		Complete(w)
}

func boolPtr(b bool) *bool {
	return &b
}
