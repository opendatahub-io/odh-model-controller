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

package controllers

import (
	"errors"
	v1 "k8s.io/api/core/v1"
)

// internalModelMeshFQDN returns the fully quallified domain name of the modelmesh-serving Kubernetes
// service that is created in a namespace for reaching ModelMesh-pods.
func internalModelMeshFQDN(namespace string) string {
	return "modelmesh-serving." + namespace + ".svc.cluster.local"
}

// getIstioGatewaysForNamespace returns the list of gateways that should be associated to a
// VirtualService to publicly expose InferenceServices living in the specified namespace.
func getIstioGatewaysForNamespace(namespace *v1.Namespace) ([]string, error) {
	if gatewayName, gwNameOk := namespace.Annotations[IstioGatewayNameAnnotation]; gwNameOk {
		return []string{gatewayName}, nil
	} else {
		return []string{}, errors.New("the " + IstioGatewayNameAnnotation + " annotation is not set on the namespace")
	}
}
