package e2e

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/kubeflow/model-registry/pkg/openapi"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/opendatahub-io/odh-model-controller/test/utils"
)

var (
	err                 error
	modelRegistryClient *openapi.APIClient
	mlmdAddr            string
)

// model registry data
var (
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
	// longer timeouts
	longerTimeout  = time.Second * 80
	longerInterval = time.Millisecond * 200
)

// Run model registry and serving e2e test assuming the controller is already deployed in the cluster
var _ = Describe("ModelRegistry controller e2e", func() {

	BeforeEach(func() {
		// Deploy model registry
		By("starting up model registry")
		modelRegistryClient = deployAndCheckModelRegistry()
	})

	AfterEach(func() {
		// Cleanup model registry
		By("tearing down model registry")
		undeployModelRegistry()
	})

	It("the controller should not create InferenceService when registered model id is missing", func() {
		_, err := utils.Run(exec.Command(kubectl, "apply", "-f", InferenceServiceWithoutRegisteredModelPath))
		Expect(err).ToNot(HaveOccurred())

		Consistently(func() error {
			// incremental id
			_, _, err := modelRegistryClient.ModelRegistryServiceAPI.GetInferenceService(ctx, "4").Execute()
			return err
		}, time.Second*20, interval).Should(HaveOccurred())
	})

	When("ISVC is created in the cluster", func() {
		BeforeEach(func() {
			// fill mr with some models
			fillModelRegistryContent(modelRegistryClient)
			// Ensure IDs in model registry are created in a specific order
			Expect(registeredModel.GetId()).To(Equal("1"))
			Expect(modelVersion.GetId()).To(Equal("2"))
			Expect(modelArtifact.GetId()).To(Equal("1"))

			_, err := utils.Run(exec.Command(kubectl, "apply", "-f", ServingRuntimePath1))
			Expect(err).ToNot(HaveOccurred())
		})

		AfterEach(func() {
			By("removing finalizers from inference service")
			_, err := utils.Run(exec.Command(kubectl, "patch", "inferenceservice", "dummy-inference-service",
				"--type", "json", "--patch", "[ { \"op\": \"remove\", \"path\": \"/metadata/finalizers\" } ]"))
			Expect(err).ToNot(HaveOccurred())
		})

		It("the controller should create InferenceService with specific model version in model registry", func() {
			_, err := utils.Run(exec.Command(kubectl, "apply", "-f", InferenceServiceWithModelVersionPath))
			Expect(err).ToNot(HaveOccurred())

			var is *openapi.InferenceService
			// incremental id
			isId := "4"
			Eventually(func() error {
				is, _, err = modelRegistryClient.ModelRegistryServiceAPI.GetInferenceService(ctx, isId).Execute()
				return err
			}, timeout, interval).ShouldNot(HaveOccurred())

			Expect(strings.HasPrefix(*is.Name, inferenceServiceName)).To(BeTrue())
			Expect(is.ServingEnvironmentId).To(Equal("3"))
			Expect(is.RegisteredModelId).To(Equal(*registeredModel.Id))
			Expect(is.ModelVersionId).ToNot(BeNil())
			Expect(*is.ModelVersionId).To(Equal(*modelVersion.Id))

			By("checking that the controller has correctly put the InferenceService id label in the ISVC")
			actualISVC := &kservev1beta1.InferenceService{}
			Eventually(func() bool {
				namespacedNamed := types.NamespacedName{Name: inferenceServiceName, Namespace: WorkingNamespace}
				err := cli.Get(ctx, namespacedNamed, actualISVC)
				if err != nil {
					return false
				}
				_, ok := actualISVC.Labels[constants.ModelRegistryInferenceServiceIdLabel]
				return ok
			}, timeout, interval).Should(BeTrue())

			Expect(actualISVC.Labels[constants.ModelRegistryRegisteredModelIdLabel]).To(Equal("1"))
			Expect(actualISVC.Labels[constants.ModelRegistryModelVersionIdLabel]).To(Equal("2"))
			Expect(actualISVC.Finalizers[0]).To(Equal("modelregistry.opendatahub.io/finalizer"))

		})

		It("the controller should get InferenceService if already existing in the model registry", func() {
			_, err := utils.Run(exec.Command(kubectl, "apply", "-f", InferenceServiceWithModelVersionPath))
			Expect(err).ToNot(HaveOccurred())

			var is *openapi.InferenceService
			// incremental id
			isId := "4"
			Eventually(func() error {
				is, _, err = modelRegistryClient.ModelRegistryServiceAPI.GetInferenceService(ctx, isId).Execute()
				return err
			}, timeout, interval).ShouldNot(HaveOccurred())

			Expect(strings.HasPrefix(*is.Name, inferenceServiceName)).To(BeTrue())
			Expect(is.ServingEnvironmentId).To(Equal("3"))
			Expect(is.RegisteredModelId).To(Equal(*registeredModel.Id))
			Expect(is.ModelVersionId).ToNot(BeNil())
			Expect(*is.ModelVersionId).To(Equal(*modelVersion.Id))

			By("checking that the controller has correctly put the InferenceService id label in the ISVC")
			actualISVC := &kservev1beta1.InferenceService{}
			Eventually(func() bool {
				namespacedNamed := types.NamespacedName{Name: inferenceServiceName, Namespace: WorkingNamespace}
				err := cli.Get(ctx, namespacedNamed, actualISVC)
				if err != nil {
					return false
				}
				_, ok := actualISVC.Labels[constants.ModelRegistryInferenceServiceIdLabel]
				return ok
			}, timeout, interval).Should(BeTrue())

			// Remove the label from ISVC which should trigger reconciliation and check it is added again
			delete(actualISVC.Labels, constants.ModelRegistryInferenceServiceIdLabel)
			Expect(cli.Update(ctx, actualISVC)).ToNot(HaveOccurred())

			Eventually(func() bool {
				namespacedNamed := types.NamespacedName{Name: inferenceServiceName, Namespace: WorkingNamespace}
				err := cli.Get(ctx, namespacedNamed, actualISVC)
				if err != nil {
					return false
				}
				_, ok := actualISVC.Labels[constants.ModelRegistryInferenceServiceIdLabel]
				return ok
			}, timeout, interval).Should(BeFalse())

			Eventually(func() bool {
				namespacedNamed := types.NamespacedName{Name: inferenceServiceName, Namespace: WorkingNamespace}
				err := cli.Get(ctx, namespacedNamed, actualISVC)
				if err != nil {
					return false
				}
				_, ok := actualISVC.Labels[constants.ModelRegistryInferenceServiceIdLabel]
				return ok
			}, timeout, interval).Should(BeTrue())

			Expect(actualISVC.Labels[constants.ModelRegistryRegisteredModelIdLabel]).To(Equal("1"))
			Expect(actualISVC.Labels[constants.ModelRegistryModelVersionIdLabel]).To(Equal("2"))
			Expect(actualISVC.Finalizers[0]).To(Equal("modelregistry.opendatahub.io/finalizer"))

		})

		It("the controller should create InferenceService without specific model version in model registry", func() {
			_, err := utils.Run(exec.Command(kubectl, "apply", "-f", InferenceServiceWithoutModelVersionPath))
			Expect(err).ToNot(HaveOccurred())

			var is *openapi.InferenceService
			// incremental id
			isId := "4"
			Eventually(func() error {
				is, _, err = modelRegistryClient.ModelRegistryServiceAPI.GetInferenceService(ctx, isId).Execute()
				return err
			}, timeout, interval).ShouldNot(HaveOccurred())

			Expect(strings.HasPrefix(*is.Name, inferenceServiceName)).To(BeTrue())
			Expect(is.ServingEnvironmentId).To(Equal("3"))
			Expect(is.RegisteredModelId).To(Equal(*registeredModel.Id))
			Expect(is.ModelVersionId).To(BeNil())

			By("checking that the controller has correctly put the InferenceService id label in the ISVC")
			actualISVC := &kservev1beta1.InferenceService{}
			Eventually(func() bool {
				namespacedNamed := types.NamespacedName{Name: inferenceServiceName, Namespace: WorkingNamespace}
				err := cli.Get(ctx, namespacedNamed, actualISVC)
				if err != nil {
					return false
				}
				_, ok := actualISVC.Labels[constants.ModelRegistryInferenceServiceIdLabel]
				return ok
			}, timeout, interval).Should(BeTrue())

			Expect(actualISVC.Labels[constants.ModelRegistryRegisteredModelIdLabel]).To(Equal("1"))
			Expect(actualISVC.Labels[constants.ModelRegistryModelVersionIdLabel]).To(Equal(""))
			Expect(actualISVC.Finalizers[0]).To(Equal("modelregistry.opendatahub.io/finalizer"))
		})
	})

	When("ISVC is deleted from the cluster", func() {
		var inferenceService *openapi.InferenceService
		BeforeEach(func() {
			// fill mr with some models
			fillModelRegistryContent(modelRegistryClient)
			// Ensure IDs in model registry are created in a specific order
			Expect(registeredModel.GetId()).To(Equal("1"))
			Expect(modelVersion.GetId()).To(Equal("2"))
			Expect(modelArtifact.GetId()).To(Equal("1"))

			// simulate ServingEnvironment creation
			envName := WorkingNamespace
			_, _, err := modelRegistryClient.ModelRegistryServiceAPI.CreateServingEnvironment(ctx).
				ServingEnvironmentCreate(openapi.ServingEnvironmentCreate{
					Name: envName,
				}).Execute()
			Expect(err).ToNot(HaveOccurred())

			inferenceService, _, err = modelRegistryClient.ModelRegistryServiceAPI.CreateInferenceService(ctx).
				InferenceServiceCreate(openapi.InferenceServiceCreate{
					Name:                 &versionName,
					DesiredState:         openapi.INFERENCESERVICESTATE_DEPLOYED.Ptr(),
					RegisteredModelId:    *registeredModel.Id,
					ServingEnvironmentId: "3",
				}).Execute()
			Expect(err).ToNot(HaveOccurred())
			Expect(inferenceService.GetId()).To(Equal("4"))

			_, err = utils.Run(exec.Command(kubectl, "apply", "-f", ServingRuntimePath1))
			Expect(err).ToNot(HaveOccurred())

			_, err = utils.Run(exec.Command(kubectl, "apply", "-f", InferenceServiceWithInfServiceIdPath))
			Expect(err).ToNot(HaveOccurred())
		})

		It("the controller should set the InferenceService desired state to UNDEPLOYED", func() {
			_, err := utils.Run(exec.Command(kubectl, "delete", "-f", InferenceServiceWithInfServiceIdPath))
			Expect(err).ToNot(HaveOccurred())

			var is *openapi.InferenceService
			// incremental id
			isId := "4"
			Eventually(func() bool {
				is, _, err = modelRegistryClient.ModelRegistryServiceAPI.GetInferenceService(ctx, isId).Execute()
				if err != nil {
					return false
				}
				return is.GetDesiredState() == openapi.INFERENCESERVICESTATE_UNDEPLOYED
			}, time.Second*20, interval).Should(BeTrue())

			By("checking that the ISVC is correctly deleted once finalizer is removed")
			actualISVC := &kservev1beta1.InferenceService{}
			Eventually(func() error {
				namespacedNamed := types.NamespacedName{Name: inferenceServiceName, Namespace: WorkingNamespace}
				return cli.Get(ctx, namespacedNamed, actualISVC)
			}, timeout, interval).Should(HaveOccurred())
		})
	})
})

// UTILS

// deployAndCheckModelRegistry setup model registry deployments and creates model registry client connection
func deployAndCheckModelRegistry() *openapi.APIClient {
	cmd := exec.Command(kubectl, "apply", "-f", ModelRegistryDatabaseDeploymentPath)
	_, err := utils.Run(cmd)
	Expect(err).ToNot(HaveOccurred())

	cmd = exec.Command(kubectl, "apply", "-f", ModelRegistryDeploymentPath)
	_, err = utils.Run(cmd)
	Expect(err).ToNot(HaveOccurred())

	waitForModelRegistryStartup()

	// retrieve model registry service
	opts := []client.ListOption{client.InNamespace(WorkingNamespace), client.MatchingLabels{
		"component": "model-registry",
	}}
	mrServiceList := &corev1.ServiceList{}
	err = cli.List(ctx, mrServiceList, opts...)
	Expect(err).ToNot(HaveOccurred())
	Expect(mrServiceList.Items).To(HaveLen(1))

	var restApiPort *int32
	for _, port := range mrServiceList.Items[0].Spec.Ports {
		if port.Name == "http-api" {
			restApiPort = &port.NodePort
			break
		}
	}
	Expect(restApiPort).ToNot(BeNil())

	mlmdAddr = fmt.Sprintf("localhost:%d", *restApiPort)

	cfg := &openapi.Configuration{
		Servers: openapi.ServerConfigurations{
			{
				URL: mlmdAddr,
			},
		},
	}

	client := openapi.NewAPIClient(cfg)
	Expect(client).ToNot(BeNil())

	return client
}

// undeployModelRegistry cleanup model registry deployments
func undeployModelRegistry() {
	cmd := exec.Command(kubectl, "delete", "-f", ModelRegistryDeploymentPath)
	_, err = utils.Run(cmd)
	Expect(err).ToNot(HaveOccurred())

	cmd = exec.Command(kubectl, "delete", "-f", ModelRegistryDatabaseDeploymentPath)
	_, err = utils.Run(cmd)
	Expect(err).ToNot(HaveOccurred())
}

// waitForModelRegistryStartup checks and block the execution until model registry is up and running
func waitForModelRegistryStartup() {
	By("by checking that the model registry database is up and running")
	Eventually(func() bool {
		opts := []client.ListOption{client.InNamespace(WorkingNamespace), client.MatchingLabels{
			"name": "model-registry-db",
		}}
		podList := &corev1.PodList{}
		err := cli.List(ctx, podList, opts...)
		if err != nil || len(podList.Items) == 0 {
			return false
		}
		return getPodReadyCondition(&podList.Items[0])
	}, longerTimeout, longerInterval).Should(BeTrue())

	By("by checking that the model registry proxy and mlmd is up and running")
	Eventually(func() bool {
		opts := []client.ListOption{client.InNamespace(WorkingNamespace), client.MatchingLabels{
			"component": "model-registry",
		}}
		podList := &corev1.PodList{}
		err := cli.List(ctx, podList, opts...)
		if err != nil || len(podList.Items) == 0 {
			return false
		}
		return getPodReadyCondition(&podList.Items[0])
	}, longerTimeout, longerInterval).Should(BeTrue())
}

// getPodReadyCondition retrieves the Pod ready condition as bool
func getPodReadyCondition(pod *corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		// fmt.Fprintf(GinkgoWriter, "Checking %s = %v\n", c.Type, c.Status)
		if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func fillModelRegistryContent(mr *openapi.APIClient) {
	// envName := WorkingNamespace
	// servingEnvironment, err = mr.GetServingEnvironmentByParams(&envName, nil)
	// Expect(err).ToNot(HaveOccurred())

	registeredModel, _, err = mr.ModelRegistryServiceAPI.FindRegisteredModel(ctx).Name(modelName).Execute()
	if err != nil {
		// register a new model
		registeredModel, _, err = mr.ModelRegistryServiceAPI.CreateRegisteredModel(ctx).RegisteredModelCreate(
			openapi.RegisteredModelCreate{
				Name: modelName,
			}).Execute()
		Expect(err).ToNot(HaveOccurred())
	}

	modelVersion, _, err = mr.ModelRegistryServiceAPI.FindModelVersion(ctx).Name(versionName).
		ParentResourceId(*registeredModel.Id).Execute()
	if err != nil {
		modelVersion, _, err = mr.ModelRegistryServiceAPI.CreateModelVersion(ctx).ModelVersionCreate(
			openapi.ModelVersionCreate{
				Name:              versionName,
				RegisteredModelId: *registeredModel.Id,
			}).Execute()
		Expect(err).ToNot(HaveOccurred())
	}

	modelArtifactName := fmt.Sprintf("%s-artifact", versionName)
	modelArtifact, _, err = mr.ModelRegistryServiceAPI.FindModelArtifact(ctx).Name(modelArtifactName).
		ParentResourceId(*modelVersion.Id).Execute()
	if err != nil {
		modelArtifact, _, err = mr.ModelRegistryServiceAPI.CreateModelArtifact(ctx).ModelArtifactCreate(
			openapi.ModelArtifactCreate{
				Name:               &modelArtifactName,
				ModelFormatName:    &modelFormatName,
				ModelFormatVersion: &modelFormatVersion,
				StorageKey:         &storageKey,
				StoragePath:        &storagePath,
			}).Execute()
		Expect(err).ToNot(HaveOccurred())
	}
}
