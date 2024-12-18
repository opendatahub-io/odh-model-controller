package utils

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"sigs.k8s.io/yaml"
)

func ConvertToStructuredResource(yamlContent []byte, out runtime.Object) error {
	s := runtime.NewScheme()
	RegisterSchemes(s)
	decode := serializer.NewCodecFactory(s).UniversalDeserializer().Decode
	_, _, err := decode(yamlContent, nil, out)
	return err
}

func ConvertToUnstructuredResource(yamlContent []byte, out *unstructured.Unstructured) error {
	return yaml.Unmarshal(yamlContent, &out.Object)
}
