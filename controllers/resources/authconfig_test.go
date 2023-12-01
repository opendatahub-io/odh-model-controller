package resources_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/opendatahub-io/odh-model-controller/controllers/resources"
	"github.com/pkg/errors"
	k8serror "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/apis"
	"sigs.k8s.io/yaml"
)

func TestExtractHost(t *testing.T) {
	url, _ := apis.ParseURL("https://caikit-example-isvc-kserve-demo.apps-crc.testing")
	isvc := &kservev1beta1.InferenceService{
		ObjectMeta: v1.ObjectMeta{
			Namespace: "kserve-demo",
		},
		Status: kservev1beta1.InferenceServiceStatus{
			URL: url,
		},
	}

	hs := resources.NewKServeInferenceServiceHostExtractor().Extract(isvc)
	for _, h := range hs {
		fmt.Println(h)
	}
}

/*
>> caikit-example-isvc-kserve-demo.apps-crc.testing

caikit-example-isvc.kserve-demo.svc.cluster.local
caikit-example-isvc-kserve-demo.apps-crc.testing
caikit-example-isvc-predictor-kserve-demo.apps-crc.testing
caikit-example-isvc-predictor.kserve-demo
caikit-example-isvc-predictor.kserve-demo.svc
caikit-example-isvc-predictor.kserve-demo.svc.cluster.local

*/

func TestY(t *testing.T) {
	store := resources.NewStaticTemplateLoader()
	auth1, _ := store.Load(context.Background(), resources.Anonymous, types.NamespacedName{})
	auth2, _ := store.Load(context.Background(), resources.Anonymous, types.NamespacedName{})

	fmt.Println(reflect.DeepEqual(auth1.Spec, auth2.Spec))
}

func TestX(t *testing.T) {

	err := k8serror.NewNotFound(schema.GroupResource{}, "test")
	errw := errors.Wrap(err, "x")

	fmt.Println(k8serror.IsNotFound(errw))
}

func TestLoadTemplateAnonymous(t *testing.T) {

	loader := resources.NewStaticTemplateLoader()
	config, err := loader.Load(context.Background(), resources.Anonymous, types.NamespacedName{})
	if err != nil {
		t.Error(err)
	}

	printYaml(t, config.Spec)
}

func TestLoadTemplateUserDefined(t *testing.T) {

	loader := resources.NewStaticTemplateLoader()
	config, err := loader.Load(context.Background(), resources.UserDefined, types.NamespacedName{})
	if err != nil {
		t.Error(err)
	}
	printYaml(t, config.Spec)
}

func printYaml(t *testing.T, o interface{}) {
	b, err := yaml.Marshal(o)
	if err != nil {
		t.Error(err)
	}
	fmt.Println(string(b))
}
