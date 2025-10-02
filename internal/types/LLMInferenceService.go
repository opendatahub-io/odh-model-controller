package types

import (
	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"knative.dev/pkg/kmeta"
)

type LLMInferenceService struct {
	*v1alpha1.LLMInferenceService
}

func (llmisvc *LLMInferenceService) GetMaaSRoleName() string {
	return kmeta.ChildName(llmisvc.Name, "-model-post-access")
}

func (llmisvc *LLMInferenceService) GetMaaSRoleBindingName() string {
	return kmeta.ChildName(llmisvc.Name, "-tier-binding")
}
