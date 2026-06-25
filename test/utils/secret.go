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

package utils

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/opendatahub-io/odh-model-controller/internal/webhook/connectionapi"
)

// BuildSecret creates a Secret with a connection-type-protocol annotation for use in webhook tests.
//
// Parameters:
//   - name: Secret name
//   - namespace: Secret namespace
//   - connType: value for the connection-type-protocol annotation
//   - data: Secret data map
//
// Returns the constructed Secret.
func BuildSecret(name, namespace, connType string, data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Annotations: map[string]string{
				connectionapi.AnnotationConnectionTypeProtocol: connType,
			},
		},
		Data: data,
	}
}

// BuildSecretWithRef creates a Secret with the deprecated connection-type-ref annotation for use
// in backward-compatibility webhook tests.
//
// Parameters:
//   - name: Secret name
//   - namespace: Secret namespace
//   - refType: value for the connection-type-ref annotation
//   - data: Secret data map
//
// Returns the constructed Secret.
func BuildSecretWithRef(name, namespace, refType string, data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Annotations: map[string]string{
				connectionapi.AnnotationConnectionTypeRef: refType,
			},
		},
		Data: data,
	}
}
