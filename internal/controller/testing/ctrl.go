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

package testing

import (
	"path/filepath"

	authorinooperatorv1beta1 "github.com/kuadrant/authorino-operator/api/v1beta1"
	kuadrantv1 "github.com/kuadrant/kuadrant-operator/api/v1"
	kuadrantv1beta1 "github.com/kuadrant/kuadrant-operator/api/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"

	istioclientv1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	igwapi "sigs.k8s.io/gateway-api-inference-extension/api/v1alpha2"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// NewEnvTest prepares k8s EnvTest with prereq
func NewEnvTest(options ...Option) *Config {
	testCRDs := WithCRDs(
		filepath.Join(ProjectRoot(), "test", "crds"),
	)
	schemes := WithScheme(
		// KServe Schemes
		kservev1alpha1.AddToScheme,
		// Kubernetes Schemes
		corev1.AddToScheme,
		rbacv1.AddToScheme,
		appsv1.AddToScheme,
		apiextv1.AddToScheme,
		netv1.AddToScheme,
		kuadrantv1.AddToScheme,
		kuadrantv1beta1.AddToScheme,
		authorinooperatorv1beta1.AddToScheme,
		gatewayapiv1.Install,
		igwapi.Install,
		istioclientv1alpha3.AddToScheme,
	)

	return Configure(append(options, testCRDs, schemes)...)
}
