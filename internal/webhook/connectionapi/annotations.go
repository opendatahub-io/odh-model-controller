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

package connectionapi

// Annotation constants used by the ConnectionsAPI feature to associate data connections with
// InferenceService and LLMInferenceService resources.
const (
	// AnnotationConnections holds the name of the Secret containing connection credentials.
	AnnotationConnections = "opendatahub.io/connections"

	// AnnotationConnectionTypeProtocol holds the connection type on the Secret (s3, uri, oci).
	AnnotationConnectionTypeProtocol = "opendatahub.io/connection-type-protocol"

	// AnnotationConnectionTypeRef is the deprecated equivalent of AnnotationConnectionTypeProtocol
	// (values: s3, uri-v1, oci-v1). Supported for backward compatibility.
	AnnotationConnectionTypeRef = "opendatahub.io/connection-type-ref"

	// AnnotationConnectionPath holds the S3 bucket sub-path for the model location.
	AnnotationConnectionPath = "opendatahub.io/connection-path"
)
