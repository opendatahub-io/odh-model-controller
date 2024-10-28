// Copyright (c) 2024 Red Hat, Inc.

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

// +groupName=nim.opendatahub.io
// +kubebuilder:object:generate=true
// +kubebuilder:validation:Required

var (
	GroupVersion  = schema.GroupVersion{Group: "nim.opendatahub.io", Version: "v1alpha1"}
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}
	Install       = SchemeBuilder.AddToScheme
)
