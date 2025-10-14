/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package testing

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"

	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ProjectRoot returns the root directory of the project by searching for the go.mod file up from where it is called.
func ProjectRoot() string {
	rootDir := ""

	currentDir, errCurrDir := os.Getwd()
	if errCurrDir != nil {
		panic(fmt.Sprintf("failed to get current working directory: %v", errCurrDir))
	}

	for {
		if _, err := os.Stat(filepath.Join(currentDir, "go.mod")); err == nil {
			rootDir = filepath.FromSlash(currentDir)
			break
		}

		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			break
		}

		currentDir = parentDir
	}

	if rootDir == "" {
		panic("failed to get project root directory. no go.mod file found")
	}

	return rootDir
}

// CreateNamespaceIfNotExists creates a namespace if it doesn't exist
func CreateNamespaceIfNotExists(ctx context.Context, c client.Client, name string) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}

	// Create namespace if it doesn't exist
	gomega.Expect(c.Create(ctx, ns)).To(gomega.Or(
		gomega.Succeed(),
		gomega.WithTransform(errors.IsAlreadyExists, gomega.BeTrue()),
	))
}

// GenerateUniqueTestName generates a unique namespace name with prefix
func GenerateUniqueTestName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, rand.Intn(99999))
}
