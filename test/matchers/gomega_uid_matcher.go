package matchers

import (
	"fmt"

	gomegatypes "github.com/onsi/gomega/types"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func HaveOwnerReferenceByUID(expectedUID types.UID) gomegatypes.GomegaMatcher {
	return &haveOwnerReferenceByUIDMatcher{expectedUID: expectedUID}
}

type haveOwnerReferenceByUIDMatcher struct {
	expectedUID types.UID
}

func (m *haveOwnerReferenceByUIDMatcher) Match(actual any) (bool, error) {
	if m.expectedUID == "" {
		return false, fmt.Errorf("HaveOwnerReferenceByUID matcher requires a non-empty UID")
	}

	obj, ok := actual.(client.Object)
	if !ok {
		return false, fmt.Errorf(
			"HaveOwnerReferenceByUID matcher expects a controller-runtime client.Object; got:\n%#v", actual)
	}

	for _, ref := range obj.GetOwnerReferences() {
		if ref.UID == m.expectedUID {
			return true, nil
		}
	}
	return false, nil
}

func (m *haveOwnerReferenceByUIDMatcher) FailureMessage(actual any) string {
	obj := actual.(client.Object)
	return fmt.Sprintf(
		"Expected\n    %#v\nto have OwnerReference with UID %q\nFound OwnerReferences:\n    %#v",
		obj, string(m.expectedUID), obj.GetOwnerReferences(),
	)
}

func (m *haveOwnerReferenceByUIDMatcher) NegatedFailureMessage(actual any) string {
	obj := actual.(client.Object)
	return fmt.Sprintf(
		"Expected\n    %#v\nnot to have OwnerReference with UID %q\nFound OwnerReferences:\n    %#v",
		obj, string(m.expectedUID), obj.GetOwnerReferences(),
	)
}
