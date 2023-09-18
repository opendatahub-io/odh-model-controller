package reconcilers

type Reconciler interface {
	Reconcile() error
}
