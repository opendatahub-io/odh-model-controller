package samples

import (
	"context"
	"io/fs"
	"strings"
	"testing"

	kservev1alpha2 "github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	"github.com/kserve/kserve/pkg/controller/v1alpha2/llmisvc"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/yaml"
)

func TestSamplesValidateAgainstCRD(t *testing.T) {
	entries, err := fs.Glob(content, "*.yaml")
	if err != nil {
		t.Fatalf("listing embedded samples: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no embedded YAML samples found")
	}

	validatorCfg := &kservev1alpha2.LLMInferenceServiceConfigValidator{}
	validatorSvc := &kservev1alpha2.LLMInferenceServiceValidator{}

	for _, name := range entries {
		t.Run(name, func(t *testing.T) {
			data, err := content.ReadFile(name)
			if err != nil {
				t.Fatalf("reading %s: %v", name, err)
			}

			var cfg kservev1alpha2.LLMInferenceServiceConfig
			if err := yaml.UnmarshalStrict(data, &cfg); err != nil {
				t.Fatalf("YAML does not match LLMInferenceServiceConfig schema: %v", err)
			}

			if cfg.APIVersion != "serving.kserve.io/v1alpha2" {
				t.Errorf("apiVersion = %q, want %q", cfg.APIVersion, "serving.kserve.io/v1alpha2")
			}
			if cfg.Kind != "LLMInferenceServiceConfig" {
				t.Errorf("kind = %q, want %q", cfg.Kind, "LLMInferenceServiceConfig")
			}
			if cfg.Name == "" {
				t.Error("metadata.name is empty")
			}

			configType := cfg.Labels["opendatahub.io/config-type"]
			if configType == "" {
				t.Error("missing label opendatahub.io/config-type")
			}

			if name != configType+".yaml" && !strings.HasPrefix(name, configType+"-") {
				t.Errorf(
					"filename %q does not match config-type label %q (expected %s.yaml or %s-*.yaml)",
					name, configType, configType, configType,
				)
			}

			if desc := cfg.Annotations["description"]; desc == "" {
				t.Error("missing or empty annotations.description")
			}

			warnings, err := validatorCfg.ValidateCreate(context.Background(), &cfg)
			if err != nil {
				t.Errorf("webhook validation failed: %v", err)
			}
			for _, w := range warnings {
				t.Logf("validation warning: %s", w)
			}

			svc := kservev1alpha2.LLMInferenceService{
				ObjectMeta: *cfg.ObjectMeta.DeepCopy(),
				Spec:       cfg.Spec,
			}
			warnings, err = validatorSvc.ValidateCreate(context.Background(), &svc)
			if err != nil {
				t.Errorf("webhook validation failed: %v", err)
			}
			for _, w := range warnings {
				t.Logf("validation warning: %s", w)
			}

			reconcilerCfg := &llmisvc.Config{
				SystemNamespace:             "kserve",
				IngressGatewayName:          "kserve-ingress-gateway",
				IngressGatewayNamespace:     "kserve",
				EnableTLS:                   true,
				ModelBasedRoutingHeaderName: "X-Gateway-Model-Name",
			}
			if _, err := llmisvc.ReplaceVariables(llmisvc.LLMInferenceServiceSample(), &cfg, reconcilerCfg); err != nil {
				t.Errorf("variable replacement validation failed: %v", err)
			}

			for k, v := range cfg.Labels {
				if errs := validation.IsQualifiedName(k); len(errs) > 0 {
					t.Errorf("label key %q is invalid: %v", k, errs)
				}
				if errs := validation.IsValidLabelValue(v); len(errs) > 0 {
					t.Errorf("invalid label %q=%q: %+v", k, v, errs)
				}
			}
			for k := range cfg.Annotations {
				if errs := validation.IsQualifiedName(k); len(errs) > 0 {
					t.Errorf("annotation key %q is invalid: %v", k, errs)
				}
			}
		})
	}
}
