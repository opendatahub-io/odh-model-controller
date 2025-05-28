// Copyright (c) 2024 Red Hat, Inc.

package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AccountSpec defines the desired state of an Account object.
type AccountSpec struct {
	// A reference to the Secret containing the NGC API Key.
	APIKeySecret corev1.ObjectReference `json:"apiKeySecret"`
	// A reference to the ConfigMap containing the list of NIM models that are allowed to be deployed.
	ModelListConfig *corev1.ObjectReference `json:"modelListConfig,omitempty"`
	// Refresh Rate for validation, defaults to 24h
	// +kubebuilder:validation:Format="duration"
	// +kubebuilder:default:="24h"
	// +kubebuilder:validation:Optional
	ValidationRefreshRate string `json:"validationRefreshRate"`
	// Refresh rate for models data, defaults to 24h
	// +kubebuilder:validation:Format="duration"
	// +kubebuilder:default:="24h"
	// +kubebuilder:validation:Optional
	NIMConfigRefreshRate string `json:"nimConfigRefreshRate"`
}

// AccountStatus defines the observed state of an Account object.
type AccountStatus struct {
	// A reference to the Template for NIM ServingRuntime.
	RuntimeTemplate *corev1.ObjectReference `json:"runtimeTemplate,omitempty"`
	// A reference to the ConfigMap with data for NIM deployment.
	NIMConfig *corev1.ObjectReference `json:"nimConfig,omitempty"`
	// A reference to the Secret for pulling NIM images.
	NIMPullSecret *corev1.ObjectReference `json:"nimPullSecret,omitempty"`
	// The last time we validated successfully.
	LastSuccessfulValidation *metav1.Time `json:"lastSuccessfulValidation,omitempty"`
	// The last time we refresh the integration config successfully.
	LastSuccessfulConfigRefresh *metav1.Time `json:"lastSuccessfulConfigRefresh,omitempty"`
	// The last time we checked the Account, failed or successful.
	LastAccountCheck *metav1.Time `json:"lastAccountCheck,omitempty"`

	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
//
// +kubebuilder:printcolumn:name="Account Status",type="string",JSONPath=".status.conditions[?(@.type==\"AccountStatus\")].status",description="Status of the Account"
// +kubebuilder:printcolumn:name="Account Check",type="date",JSONPath=".status.lastAccountCheck",description="Last account check, failed or successful"
// +kubebuilder:printcolumn:name="Validation Rate",type="string",JSONPath=".spec.validationRefreshRate",description="Validation refresh rate"
// +kubebuilder:printcolumn:name="Last Successful Validation",type="date",JSONPath=".status.lastSuccessfulValidation",description="Last successful validation"
// +kubebuilder:printcolumn:name="Config Rate",type="string",JSONPath=".spec.nimConfigRefreshRate",description="Config refresh rate"
// +kubebuilder:printcolumn:name="Last Successful Config",type="date",JSONPath=".status.lastSuccessfulConfigRefresh",description="Last successful config refresh"

// Account is used for adopting a NIM Account for Open Data Hub.
type Account struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AccountSpec   `json:"spec,omitempty"`
	Status AccountStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AccountList is used for encapsulating Account items.
type AccountList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Account `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Account{}, &AccountList{})
}
