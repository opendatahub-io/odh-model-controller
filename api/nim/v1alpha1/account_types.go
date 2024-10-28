// Copyright (c) 2024 Red Hat, Inc.

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type (
	// AccountSpec defines the desired state of an Account object.
	AccountSpec struct {
		// A reference to the Secret containing the NGC API Key.
		SecretRef corev1.ObjectReference `json:"secretRef"`
	}

	// AccountStatus defines the observed state of an Account object.
	AccountStatus struct {
		// A reference to the Template for NIM ServingRuntime.
		TemplateRef *corev1.ObjectReference `json:"templateRef,omitempty"`
		// A reference to the ConfigMap with data for NIM deployment.
		ConfigMapRef *corev1.ObjectReference `json:"configMapRef,omitempty"`
		// A reference to the Secret for pulling NIM images.
		SecretMapRef *corev1.ObjectReference `json:"secretRef,omitempty"`

		// +operator-sdk:csv:customresourcedefinitions:type=status,xDescriptors={"urn:alm:descriptor:io.kubernetes.conditions"}
		Conditions []metav1.Condition `json:"conditions,omitempty"`
	}

	// Account is used for adopting a NIM Account for Open Data Hub.
	//
	// +kubebuilder:object:root=true
	// +kubebuilder:subresource:status
	//
	// +kubebuilder:printcolumn:name="Template",type="string",JSONPath=".status.templateRef.name",description="The name of the Template"
	// +kubebuilder:printcolumn:name="ConfigMap",type="string",JSONPath=".status.configMapRef.name",description="The name of the ConfigMap"
	//
	// +operator-sdk:csv:customresourcedefinitions:displayName="Account"
	// +operator-sdk:csv:customresourcedefinitions:resources={{ConfigMap,v1},{Secret,v1},{Template,template.openshift.io/v1}}
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
