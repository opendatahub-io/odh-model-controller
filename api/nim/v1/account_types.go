// Copyright (c) 2024 Red Hat, Inc.

package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type (
	// AccountSpec defines the desired state of an Account object.
	AccountSpec struct {
		// A reference to the Secret containing the NGC API Key.
		APIKeySecret corev1.ObjectReference `json:"apiKeySecret"`
	}

	// AccountStatus defines the observed state of an Account object.
	AccountStatus struct {
		// A reference to the Template for NIM ServingRuntime.
		RuntimeTemplate *corev1.ObjectReference `json:"runtimeTemplate,omitempty"`
		// A reference to the ConfigMap with data for NIM deployment.
		NIMConfig *corev1.ObjectReference `json:"nimConfig,omitempty"`
		// A reference to the Secret for pulling NIM images.
		NIMPullSecret *corev1.ObjectReference `json:"nimPullSecret,omitempty"`

		Conditions []metav1.Condition `json:"conditions,omitempty"`
	}

	// Account is used for adopting a NIM Account for Open Data Hub.
	//
	// +kubebuilder:object:root=true
	// +kubebuilder:subresource:status
	//
	// +kubebuilder:printcolumn:name="Template",type="string",JSONPath=".status.runtimeTemplate.name",description="Template for ServingRuntime"
	// +kubebuilder:printcolumn:name="ConfigMap",type="string",JSONPath=".status.nimConfig.name",description="ConfigMap of NIM data"
	// +kubebuilder:printcolumn:name="Secret",type="string",JSONPath=".status.nimPullSecret.name",description="Secret for pulling NIM images"
	Account struct {
		metav1.TypeMeta   `json:",inline"`
		metav1.ObjectMeta `json:"metadata,omitempty"`

		Spec   AccountSpec   `json:"spec,omitempty"`
		Status AccountStatus `json:"status,omitempty"`
	}

	// AccountList is used for encapsulating Account items.
	//
	// +kubebuilder:object:root=true
	AccountList struct {
		metav1.TypeMeta `json:",inline"`
		metav1.ListMeta `json:"metadata,omitempty"`
		Items           []Account `json:"items"`
	}
)

func init() {
	SchemeBuilder.Register(&Account{}, &AccountList{})
}
