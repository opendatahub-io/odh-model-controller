/*

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

package controllers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/opendatahub-io/model-registry/pkg/api"
	"github.com/opendatahub-io/model-registry/pkg/core"
	"github.com/opendatahub-io/model-registry/pkg/openapi"
	"github.com/opendatahub-io/odh-model-controller/controllers/constants"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"k8s.io/apimachinery/pkg/types"
)

var (
	mlmdAddr     string
	mlmdTeardown func() error
	// mr content
	registeredModel *openapi.RegisteredModel
	modelVersion    *openapi.ModelVersion
	modelArtifact   *openapi.ModelArtifact
	// data
	modelName            = "dummy-model"
	versionName          = "dummy-version"
	modelFormatName      = "onnx"
	modelFormatVersion   = "1"
	storagePath          = "path/to/model"
	storageKey           = "aws-connection-models"
	inferenceServiceName = "dummy-inference-service"
	// filled at runtime
	servingRuntime = &kservev1alpha1.ServingRuntime{}
)

const (
	useProvider             = testcontainers.ProviderDefault // or explicit to testcontainers.ProviderPodman if needed
	mlmdImage               = "gcr.io/tfx-oss-public/ml_metadata_store_server:1.14.0"
	modelRegistryImage      = "quay.io/opendatahub/model-registry:latest"
	connectionConfigContent = `connection_config {
  sqlite {
    filename_uri: '/tmp/mlmd/odh_metadata.sqlite-%s.db'
    connection_mode: READWRITE_OPENCREATE
  }
}
`
)

var _ = Describe("ModelRegistry controller", func() {
	ctx := context.Background()
	var mrService api.ModelRegistryApi

	BeforeEach(func() {
		var err error

		ctxTimeout, cancel := context.WithTimeout(ctx, time.Second*5)
		defer cancel()

		// Setup testcontainer with ml-metadata
		mlmdAddr, mlmdTeardown, err = setupMlMetadataContainer()
		Expect(err).NotTo(HaveOccurred())
		// override the mlmd address setting the env variable
		os.Setenv(constants.MLMDAddressEnv, mlmdAddr)

		conn, err := grpc.DialContext(
			ctxTimeout,
			mlmdAddr,
			grpc.WithReturnConnectionError(),
			grpc.WithBlock(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		Expect(err).ToNot(HaveOccurred())

		mrService, err = core.NewModelRegistryService(conn)
		Expect(err).ToNot(HaveOccurred())
		Expect(mrService).ToNot(BeNil())

		servingRuntime = &kservev1alpha1.ServingRuntime{}
		err = convertToStructuredResource(KserveServingRuntimePath1, servingRuntime)
		Expect(err).NotTo(HaveOccurred())
		Expect(cli.Create(ctx, servingRuntime)).Should(Succeed())

		// fill mr with some models
		registeredModel, modelVersion, modelArtifact, err = setupModels(mrService)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		By("Tearing down model registry test container")
		Expect(mlmdTeardown()).NotTo(HaveOccurred())
	})

	When("when a ServingRuntime is applied in WorkingNamespace", func() {
		It("should create and delete InferenceService CRs based on model registry content", func() {
			By("by checking that the controller has created the corresponding ServingEnvironment in model registry")
			envId := ""
			Eventually(func() error {
				ns := WorkingNamespace
				se, err := mrService.GetServingEnvironmentByParams(&ns, nil)
				if err != nil {
					return err
				}
				envId = *se.Id
				return nil
			}, timeout, interval).ShouldNot(HaveOccurred())

			When("an InferenceService is created in the model registry with existing model runtime", func() {
				By("by checking that the controller has created the InferenceService CR in WorkingNamespace")
				// create the IS in the model registry
				is, err := mrService.UpsertInferenceService(&openapi.InferenceService{
					Name:                 &inferenceServiceName,
					RegisteredModelId:    registeredModel.GetId(),
					ServingEnvironmentId: envId,
					Runtime:              &servingRuntime.Name,
					State:                openapi.INFERENCESERVICESTATE_DEPLOYED.Ptr(),
				})
				Expect(err).NotTo(HaveOccurred())
				isvc := &kservev1beta1.InferenceService{}
				Eventually(func() error {
					key := types.NamespacedName{Name: inferenceServiceName, Namespace: WorkingNamespace}
					return cli.Get(ctx, key, isvc)
				}, timeout, interval).ShouldNot(HaveOccurred())

				By("by checking that the controller has removed the InferenceService CR from WorkingNamespace")
				// set state to UNDEPLOYED for IS in the model registry
				is.SetState(openapi.INFERENCESERVICESTATE_UNDEPLOYED)
				_, err = mrService.UpsertInferenceService(is)
				Expect(err).NotTo(HaveOccurred())
				isvc = &kservev1beta1.InferenceService{}
				Eventually(func() error {
					key := types.NamespacedName{Name: inferenceServiceName, Namespace: WorkingNamespace}
					return cli.Get(ctx, key, isvc)
				}, timeout, interval).Should(HaveOccurred())

				By("by checking that the controller has created new ServeModel in the model registry")
				Eventually(func() error {
					sm, err := mrService.GetServeModels(api.ListOptions{}, is.Id)
					if err != nil {
						return err
					}
					if sm.Size == 0 {
						return fmt.Errorf("empty serve models list")
					}
					return nil
				}, timeout, interval).ShouldNot(HaveOccurred())
			})
		})

		It("should update InferenceService CR based on model registry content", func() {
			By("by checking that the controller has created the corresponding ServingEnvironment in model registry")
			envId := ""
			Eventually(func() error {
				ns := WorkingNamespace
				se, err := mrService.GetServingEnvironmentByParams(&ns, nil)
				if err != nil {
					return err
				}
				envId = *se.Id
				return nil
			}, timeout, interval).ShouldNot(HaveOccurred())

			When("an InferenceService is created in the model registry with existing model runtime", func() {
				By("by checking that the controller has created the InferenceService CR in WorkingNamespace")
				// create the IS in the model registry
				is, err := mrService.UpsertInferenceService(&openapi.InferenceService{
					Name:                 &inferenceServiceName,
					RegisteredModelId:    registeredModel.GetId(),
					ServingEnvironmentId: envId,
					Runtime:              &servingRuntime.Name,
					State:                openapi.INFERENCESERVICESTATE_DEPLOYED.Ptr(),
				})
				Expect(err).NotTo(HaveOccurred())
				isvc := &kservev1beta1.InferenceService{}
				Eventually(func() error {
					key := types.NamespacedName{Name: inferenceServiceName, Namespace: WorkingNamespace}
					return cli.Get(ctx, key, isvc)
				}, timeout, interval).ShouldNot(HaveOccurred())

				By("by checking that the controller has correctly updated the InferenceService CR in WorkingNamespace")
				// update model artifact content
				newStoragePath := "/new/path"
				modelArtifact.SetStoragePath(newStoragePath)
				modelArtifact, err = mrService.UpsertModelArtifact(modelArtifact, modelVersion.Id)
				Expect(err).NotTo(HaveOccurred())

				isvc = &kservev1beta1.InferenceService{}
				Eventually(func() string {
					key := types.NamespacedName{Name: inferenceServiceName, Namespace: WorkingNamespace}
					err := cli.Get(ctx, key, isvc)
					if err != nil {
						return ""
					}
					return *isvc.Spec.Predictor.Model.Storage.Path
				}, timeout, interval).Should(Equal(newStoragePath))

				By("by checking that the controller has created new ServeModel in the model registry")
				Eventually(func() error {
					sm, err := mrService.GetServeModels(api.ListOptions{}, is.Id)
					if err != nil {
						return err
					}
					if sm.Size == 0 {
						return fmt.Errorf("empty serve models list")
					}
					return nil
				}, timeout, interval).ShouldNot(HaveOccurred())
			})
		})
	})
})

func setupModels(mr api.ModelRegistryApi) (*openapi.RegisteredModel, *openapi.ModelVersion, *openapi.ModelArtifact, error) {
	model, err := mr.GetRegisteredModelByParams(&modelName, nil)
	if err != nil {
		// register a new model
		model, err = mr.UpsertRegisteredModel(&openapi.RegisteredModel{
			Name: &modelName,
		})
		if err != nil {
			return nil, nil, nil, err
		}
	}

	version, err := mr.GetModelVersionByParams(&versionName, model.Id, nil)
	if err != nil {
		version, err = mr.UpsertModelVersion(&openapi.ModelVersion{
			Name: &versionName,
		}, model.Id)
		if err != nil {
			return nil, nil, nil, err
		}
	}

	modelArtifactName := fmt.Sprintf("%s-artifact", versionName)
	artifact, err := mr.GetModelArtifactByParams(&modelArtifactName, version.Id, nil)
	if err != nil {
		artifact, err = mr.UpsertModelArtifact(&openapi.ModelArtifact{
			Name:               &modelArtifactName,
			ModelFormatName:    &modelFormatName,
			ModelFormatVersion: &modelFormatVersion,
			StorageKey:         &storageKey,
			StoragePath:        &storagePath,
		}, version.Id)
		if err != nil {
			return nil, nil, nil, err
		}
	}

	return model, version, artifact, nil
}

func setupMlMetadataContainer() (string, func() error, error) {
	mlmdCtx := context.TODO()
	connConfigFile, err := os.CreateTemp("", "odh_mlmd_conn_config-*.pb")
	if err != nil {
		return "", nil, err
	}
	uid := strings.Replace(strings.Split(filepath.Base(connConfigFile.Name()), "-")[1], ".pb", "", 1)
	_, err = connConfigFile.WriteString(fmt.Sprintf(connectionConfigContent, uid))
	if err != nil {
		return "", nil, err
	}

	req := testcontainers.ContainerRequest{
		Image:        mlmdImage,
		ExposedPorts: []string{"8080/tcp"},
		Env: map[string]string{
			"METADATA_STORE_SERVER_CONFIG_FILE": fmt.Sprintf("/tmp/mlmd/%s", filepath.Base(connConfigFile.Name())),
		},
		User: "1000:1000",
		Mounts: testcontainers.ContainerMounts{
			testcontainers.ContainerMount{
				Source: testcontainers.GenericBindMountSource{
					HostPath: os.TempDir(),
				},
				Target: "/tmp/mlmd",
			},
		},
		WaitingFor: wait.ForLog("Server listening on"),
	}

	mlmdgrpc, err := testcontainers.GenericContainer(mlmdCtx, testcontainers.GenericContainerRequest{
		ProviderType:     useProvider,
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return "", nil, err
	}

	mappedHost, err := mlmdgrpc.Host(mlmdCtx)
	if err != nil {
		return "", nil, err
	}
	mappedPort, err := mlmdgrpc.MappedPort(mlmdCtx, "8080")
	if err != nil {
		return "", nil, err
	}

	return fmt.Sprintf("%s:%s", mappedHost, mappedPort.Port()), func() (err error) {
		return mlmdgrpc.Terminate(mlmdCtx)
	}, nil
}
