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

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// ConnectionInfo holds connection-related information extracted from resource annotations and the referenced Secret.
type ConnectionInfo struct {
	// SecretName is the name of the Secret from the opendatahub.io/connections annotation.
	SecretName string
	// Type is the connection type from the connection-type-protocol or connection-type-ref annotation on the Secret.
	Type string
	// Path is the S3 sub-path from the opendatahub.io/connection-path annotation on the resource.
	Path string
}

// ConnectionAction represents the mutation action to perform on a resource during admission.
type ConnectionAction string

const (
	// ConnectionActionInject injects connection credentials into the resource.
	ConnectionActionInject ConnectionAction = "inject"
	// ConnectionActionRemove removes previously injected connection credentials.
	ConnectionActionRemove ConnectionAction = "remove"
	// ConnectionActionReplace removes old credentials and injects new ones.
	ConnectionActionReplace ConnectionAction = "replace"
	// ConnectionActionNone indicates no mutation is needed.
	ConnectionActionNone ConnectionAction = "none"
)

// ConnectionType is the canonical identifier for a connection protocol.
type ConnectionType string

const (
	// ConnectionTypeProtocolS3 identifies S3-compatible object storage connections.
	ConnectionTypeProtocolS3 ConnectionType = "s3"
	// ConnectionTypeProtocolURI identifies generic URI connections.
	ConnectionTypeProtocolURI ConnectionType = "uri"
	// ConnectionTypeProtocolOCI identifies OCI container image connections.
	ConnectionTypeProtocolOCI ConnectionType = "oci"

	// ConnectionTypeRefS3 is the deprecated ref value for S3 connections.
	ConnectionTypeRefS3 ConnectionType = "s3"
	// ConnectionTypeRefURI is the deprecated ref value for URI connections.
	ConnectionTypeRefURI ConnectionType = "uri-v1"
	// ConnectionTypeRefOCI is the deprecated ref value for OCI connections.
	ConnectionTypeRefOCI ConnectionType = "oci-v1"
)

// String returns the string representation of the ConnectionType.
func (ct ConnectionType) String() string {
	return string(ct)
}

// ValidateConnectionAnnotation validates the opendatahub.io/connections annotation on obj.
//
// Fetches the referenced Secret once and validates the connection type from its annotations.
// Returns populated ConnectionInfo and the fetched Secret when a valid connection is found.
// Returns empty ConnectionInfo and a nil Secret (no error) when no injection should be performed
// (annotation absent, unknown type, or no type annotation on the Secret).
// Returns an error when validation fails (e.g., Secret not found, API error).
//
// The returned Secret is non-nil only when injection should proceed; callers must pass it to
// injection helpers (GetURIValue, BuildS3URI) to avoid a second fetch.
//
// Parameters:
//   - ctx: context for logging and API calls
//   - apiReader: uncached reader for fetching the Secret
//   - obj: the admitted resource (must implement metav1.Object)
//   - namespace: namespace of the admitted resource
//
// Returns the validated ConnectionInfo, the fetched Secret, and any error encountered.
func ValidateConnectionAnnotation(ctx context.Context, apiReader client.Reader, obj metav1.Object, namespace string) (ConnectionInfo, *corev1.Secret, error) {
	log := logf.FromContext(ctx)

	secretName := obj.GetAnnotations()[AnnotationConnections]
	if secretName == "" {
		return ConnectionInfo{}, nil, nil
	}

	secret := &corev1.Secret{}
	if err := apiReader.Get(ctx, types.NamespacedName{Name: secretName, Namespace: namespace}, secret); err != nil {
		if k8serr.IsNotFound(err) {
			return ConnectionInfo{}, nil, fmt.Errorf("connection %q referenced by annotation %q not found in namespace %q",
				secretName, AnnotationConnections, namespace)
		}
		log.Error(err, "failed to get connection", "connection", secretName, "namespace", namespace)
		return ConnectionInfo{}, nil, fmt.Errorf("failed to validate connection: %w", err)
	}

	connType, isValid := ValidateConnectionType(secret)
	if connType == "" {
		log.Info("connection has no type annotation, skipping injection", "connection", secretName)
		return ConnectionInfo{}, nil, nil
	}
	if !isValid {
		log.Info("unknown connection type, skipping injection", "connectionType", connType, "connection", secretName)
		return ConnectionInfo{}, nil, nil
	}

	connPath := obj.GetAnnotations()[AnnotationConnectionPath]
	return ConnectionInfo{
		SecretName: secret.Name,
		Type:       connType,
		Path:       connPath,
	}, secret, nil
}

// ValidateConnectionType checks connection type annotations on obj.
//
// Prefers connection-type-protocol over the deprecated connection-type-ref. Returns the type
// string and a flag indicating whether the type is a known, supported connection type.
//
// Parameters:
//   - obj: any object whose annotations carry the connection type (e.g., *corev1.Secret)
//
// Returns the connection type string (empty if no annotation) and whether it is a valid known type.
func ValidateConnectionType(obj metav1.Object) (string, bool) {
	allowedProtocol := []string{
		ConnectionTypeProtocolS3.String(),
		ConnectionTypeProtocolURI.String(),
		ConnectionTypeProtocolOCI.String(),
	}
	allowedRef := []string{
		ConnectionTypeRefS3.String(),
		ConnectionTypeRefURI.String(),
		ConnectionTypeRefOCI.String(),
	}

	annotations := obj.GetAnnotations()
	if connType, ok := annotations[AnnotationConnectionTypeProtocol]; ok && connType != "" {
		return connType, slices.Contains(allowedProtocol, connType)
	}
	if connType, ok := annotations[AnnotationConnectionTypeRef]; ok && connType != "" {
		return connType, slices.Contains(allowedRef, connType)
	}
	return "", false
}

// DetermineAction compares old and new ConnectionInfo to decide what mutation to perform on UPDATE.
//
// The decision table:
//   - old present, new absent → remove
//   - both absent → none
//   - old absent, new present → inject
//   - type or secret name changed → replace
//   - S3 with changed path → replace
//   - identical → none
//
// Parameters:
//   - oldConn: connection info from the previous object version
//   - newConn: connection info from the incoming object version
//
// Returns the ConnectionAction to perform.
func DetermineAction(oldConn, newConn ConnectionInfo) ConnectionAction {
	if oldConn.SecretName != "" && newConn.SecretName == "" {
		return ConnectionActionRemove
	}
	if oldConn.SecretName == "" && newConn.SecretName == "" {
		return ConnectionActionNone
	}
	if oldConn.SecretName == "" && newConn.SecretName != "" {
		return ConnectionActionInject
	}
	if oldConn.Type != newConn.Type || oldConn.SecretName != newConn.SecretName {
		return ConnectionActionReplace
	}
	isS3 := newConn.Type == ConnectionTypeProtocolS3.String() || newConn.Type == ConnectionTypeRefS3.String()
	if isS3 && oldConn.Path != newConn.Path {
		return ConnectionActionReplace
	}
	return ConnectionActionNone
}

// CreateServiceAccountIfNeeded creates a ServiceAccount for S3 connections. No-op for non-S3 types or dry-run requests.
//
// Parameters:
//   - ctx: context for API calls and logging
//   - cli: write-capable client for ServiceAccount creation
//   - secretName: name of the connection Secret (SA will be named secretName+"-sa")
//   - connType: connection type string
//   - namespace: namespace for the ServiceAccount
//   - isDryRun: when true, skips actual SA creation
//
// Returns any error from ServiceAccount creation.
func CreateServiceAccountIfNeeded(ctx context.Context, cli client.Client, secretName, connType, namespace string, isDryRun bool) error {
	log := logf.FromContext(ctx)

	isS3 := connType == ConnectionTypeProtocolS3.String() || connType == ConnectionTypeRefS3.String()
	if !isS3 {
		log.V(1).Info("skipping SA creation for non-S3 connection type", "connectionType", connType)
		return nil
	}
	if isDryRun {
		log.V(1).Info("skipping SA creation in dry-run mode", "connection", secretName)
		return nil
	}
	return createSA(ctx, cli, secretName, namespace)
}

// createSA creates a ServiceAccount linking the given secret. AlreadyExists is treated as success.
func createSA(ctx context.Context, cli client.Client, secretName, namespace string) error {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName + "-sa",
			Namespace: namespace,
		},
		Secrets: []corev1.ObjectReference{{Name: secretName}},
	}
	if err := cli.Create(ctx, sa); err != nil && !k8serr.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create ServiceAccount: %w", err)
	}
	return nil
}

// GetURIValue reads the URI value from the pre-fetched Secret's "https-host" or "URI" data key.
//
// Parameters:
//   - secret: the pre-fetched connection Secret
//
// Returns the URI string or an error if neither key is present.
func GetURIValue(secret *corev1.Secret) (string, error) {
	if v, ok := secret.Data["https-host"]; ok {
		return string(v), nil
	}
	if v, ok := secret.Data["URI"]; ok {
		return string(v), nil
	}
	return "", errors.New("connection does not contain 'https-host' or 'URI' data key")
}

// BuildS3URI constructs an S3 URI (s3://{bucket}/{path}) from the pre-fetched Secret's AWS_S3_BUCKET
// key and the connection path.
//
// Parameters:
//   - secret: the pre-fetched connection Secret
//   - connInfo: connection info containing the path
//
// Returns the S3 URI string or an error if the bucket key is missing or the path is empty.
func BuildS3URI(secret *corev1.Secret, connInfo ConnectionInfo) (string, error) {
	bucket, ok := secret.Data["AWS_S3_BUCKET"]
	if !ok {
		return "", errors.New("connection does not contain 'AWS_S3_BUCKET' data key")
	}
	if len(bucket) == 0 {
		return "", errors.New("connection 'AWS_S3_BUCKET' data key is empty")
	}
	if connInfo.Path == "" {
		return "", errors.New("connection info does not have a path")
	}
	return fmt.Sprintf("s3://%s/%s", string(bucket), connInfo.Path), nil
}

// GetOldConnectionInfo decodes req.OldObject.Raw and extracts the connection annotation and type.
//
// It reads the opendatahub.io/connections annotation and fetches the referenced Secret metadata
// to determine the old connection type. When the Secret has been deleted, Type is left empty so
// callers perform a full cleanup. Returns empty ConnectionInfo when no old connection annotation
// is present.
//
// Parameters:
//   - ctx: context for logging and API calls
//   - req: the admission request containing the old object raw bytes
//   - apiReader: uncached reader for fetching Secret metadata
//
// Returns the old ConnectionInfo and any API error encountered.
func GetOldConnectionInfo(ctx context.Context, req admission.Request, apiReader client.Reader) (ConnectionInfo, error) {
	log := logf.FromContext(ctx)

	if req.OldObject.Raw == nil {
		return ConnectionInfo{}, nil
	}

	oldObj := &metav1.PartialObjectMetadata{}
	if err := json.Unmarshal(req.OldObject.Raw, oldObj); err != nil {
		return ConnectionInfo{}, fmt.Errorf("failed to decode old object: %w", err)
	}

	secretName := oldObj.GetAnnotations()[AnnotationConnections]
	if secretName == "" {
		return ConnectionInfo{}, nil
	}

	secretMeta := &metav1.PartialObjectMetadata{}
	secretMeta.SetGroupVersionKind(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Secret"})
	if err := apiReader.Get(ctx, types.NamespacedName{Name: secretName, Namespace: req.Namespace}, secretMeta); err != nil {
		if k8serr.IsNotFound(err) {
			oldConnectionType := oldObj.GetAnnotations()[AnnotationInjectedConnectionType]
			log.V(1).Info("old secret not found, recovering type from object annotation", "connection", secretName, "recoveredType", oldConnectionType)
			return ConnectionInfo{
				SecretName: secretName,
				Type:       oldConnectionType,
				Path:       oldObj.GetAnnotations()[AnnotationConnectionPath],
			}, nil
		}
		return ConnectionInfo{}, fmt.Errorf("failed to get old secret metadata: %w", err)
	}

	connType := secretMeta.GetAnnotations()[AnnotationConnectionTypeProtocol]
	if connType == "" {
		connType = secretMeta.GetAnnotations()[AnnotationConnectionTypeRef]
	}

	return ConnectionInfo{
		SecretName: secretName,
		Type:       connType,
		Path:       oldObj.GetAnnotations()[AnnotationConnectionPath],
	}, nil
}

// InjectServiceAccountName sets serviceAccountName to saName only when it is currently empty.
// Existing user-set values are preserved.
//
// Parameters:
//   - serviceAccountName: pointer to the ServiceAccountName field to conditionally set
//   - saName: the value to inject
func InjectServiceAccountName(serviceAccountName *string, saName string) {
	if *serviceAccountName == "" {
		*serviceAccountName = saName
	}
}

// RemoveServiceAccountName clears serviceAccountName only when its current value matches injectedSAName.
// User-set values that differ from injectedSAName are preserved.
//
// Parameters:
//   - serviceAccountName: pointer to the ServiceAccountName field to conditionally clear
//   - injectedSAName: the value that was previously injected and should be removed
func RemoveServiceAccountName(serviceAccountName *string, injectedSAName string) {
	if *serviceAccountName == injectedSAName {
		*serviceAccountName = ""
	}
}

// InjectOCIImagePullSecrets appends a LocalObjectReference for secretName to imagePullSecrets
// if it is not already present (idempotent).
//
// Parameters:
//   - imagePullSecrets: pointer to the ImagePullSecrets slice to update
//   - secretName: name of the Secret to add
func InjectOCIImagePullSecrets(imagePullSecrets *[]corev1.LocalObjectReference, secretName string) {
	for _, ref := range *imagePullSecrets {
		if ref.Name == secretName {
			return
		}
	}
	*imagePullSecrets = append(*imagePullSecrets, corev1.LocalObjectReference{Name: secretName})
}

// CleanupOCIImagePullSecrets removes the LocalObjectReference for secretName from imagePullSecrets.
// No-op when secretName is empty.
//
// Parameters:
//   - imagePullSecrets: pointer to the ImagePullSecrets slice to update
//   - secretName: name of the Secret to remove
func CleanupOCIImagePullSecrets(imagePullSecrets *[]corev1.LocalObjectReference, secretName string) {
	if secretName == "" {
		return
	}
	result := make([]corev1.LocalObjectReference, 0, len(*imagePullSecrets))
	for _, ref := range *imagePullSecrets {
		if ref.Name != secretName {
			result = append(result, ref)
		}
	}
	*imagePullSecrets = result
}
