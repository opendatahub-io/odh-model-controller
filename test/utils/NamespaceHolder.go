package utils

import (
	"context"
	"math/rand"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var Namespaces NamespaceHolder

type NamespaceHolder struct {
	namespaces []string
}

func init() {
	Namespaces = NamespaceHolder{}
}

func createTestNamespaceName() string {
	n := 5
	letterRunes := []rune("abcdefghijklmnopqrstuvwxyz")

	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return "test-ns-" + string(b)
}

func (n *NamespaceHolder) Get() string {
	ns := createTestNamespaceName()
	n.namespaces = append(n.namespaces, ns)
	return ns
}

func (n *NamespaceHolder) All() []string {
	return n.namespaces
}

func (n *NamespaceHolder) Clear() {
	n.namespaces = []string{}
}

func (n *NamespaceHolder) Create(ctx context.Context, cli client.Client) *corev1.Namespace {
	testNs := Namespaces.Get()
	testNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testNs,
			Namespace: testNs,
		},
	}
	Expect(cli.Create(ctx, testNamespace)).Should(Succeed())
	return testNamespace
}
