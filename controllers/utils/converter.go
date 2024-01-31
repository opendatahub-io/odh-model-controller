package utils

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

func ConvertToStructuredResource(yamlContent []byte, out runtime.Object) error {

	s := runtime.NewScheme()
	RegisterSchemes(s)
	decode := serializer.NewCodecFactory(s).UniversalDeserializer().Decode
	_, _, err := decode(yamlContent, nil, out)
	if err != nil {
		return err
	}
	return nil
}
