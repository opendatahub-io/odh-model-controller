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

package fixture

import (
	"context"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"k8s.io/client-go/rest"

	llmcontroller "github.com/opendatahub-io/odh-model-controller/internal/controller/serving/llm"
	pkgtest "github.com/opendatahub-io/odh-model-controller/internal/controller/testing"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"

	ctrl "sigs.k8s.io/controller-runtime"
)

func SetupTestEnv() *pkgtest.Client {
	duration, err := time.ParseDuration(utils.GetEnvOr("ENVTEST_DEFAULT_TIMEOUT", "30s"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.SetDefaultEventuallyTimeout(duration)
	gomega.SetDefaultEventuallyPollingInterval(250 * time.Millisecond)
	gomega.EnforceDefaultTimeoutsWhenUsingContexts()

	ginkgo.By("Setting up the test environment")
	systemNs := utils.GetEnvOr("POD_NAMESPACE", "opendatahub")

	ctx, cancel := context.WithCancel(context.Background())
	setupLog := ctrl.Log.WithName("setup")

	llmCtrlFunc := func(mgr ctrl.Manager, cfg *rest.Config) error {
		return llmcontroller.NewLLMInferenceServiceReconciler(
			mgr.GetClient(),
			mgr.GetScheme(),
			cfg,
		).SetupWithManager(mgr, setupLog)
	}

	envTest := pkgtest.NewEnvTest().
		WithControllers(llmCtrlFunc).
		Start(ctx)

	ginkgo.DeferCleanup(func() {
		cancel()
		gomega.Expect(envTest.Stop()).To(gomega.Succeed())
	})

	RequiredResources(ctx, envTest.Client, systemNs)

	return envTest
}
