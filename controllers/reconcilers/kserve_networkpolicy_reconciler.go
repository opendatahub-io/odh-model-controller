/*

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

package reconcilers

import (
	"context"
	"github.com/go-logr/logr"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/opendatahub-io/odh-model-controller/controllers/resources"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	networkPolicyName = "allow-from-openshift-monitoring-ns"
)

type KserveNetworkPolicyReconciler struct {
	client               client.Client
	scheme               *runtime.Scheme
	networkPolicyHandler resources.NetworkPolicyHandler
}

func NewKServeNetworkPolicyReconciler(client client.Client, scheme *runtime.Scheme) *KserveNetworkPolicyReconciler {
	return &KserveNetworkPolicyReconciler{
		client:               client,
		scheme:               scheme,
		networkPolicyHandler: resources.NewNetworkPolicyHandler(client),
	}
}

func (r *KserveNetworkPolicyReconciler) Reconcile(ctx context.Context, log logr.Logger, isvc *kservev1beta1.InferenceService) error {
	return r.deleteNetworkPolicy(ctx, isvc.Namespace)
}

func (r *KserveNetworkPolicyReconciler) deleteNetworkPolicy(ctx context.Context, isvcNamespace string) error {
	return r.networkPolicyHandler.DeleteNetworkPolicy(ctx, types.NamespacedName{Name: networkPolicyName, Namespace: isvcNamespace})
}
