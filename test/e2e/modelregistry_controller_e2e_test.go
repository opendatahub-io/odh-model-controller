package e2e

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	kservev1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/opendatahub-io/model-registry/pkg/api"
	"github.com/opendatahub-io/model-registry/pkg/core"
	"github.com/opendatahub-io/model-registry/pkg/openapi"
	"github.com/opendatahub-io/odh-model-controller/controllers/constants"
	"github.com/opendatahub-io/odh-model-controller/test/utils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	err                 error
	modelRegistryClient api.ModelRegistryApi
	mlmdAddr            string
)

// model registry data
var (
	servingEnvironment *openapi.ServingEnvironment
	registeredModel    *openapi.RegisteredModel
	modelVersion       *openapi.ModelVersion
	modelArtifact      *openapi.ModelArtifact
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

	When("ISVC is created in the cluster", func() {
		BeforeEach(func() {
			// fill mr with some models
			fillModelRegistryContent(modelRegistryClient)
			// Ensure IDs in model registry are created in a specific order
			Expect(servingEnvironment.GetId()).To(Equal("1"))
			Expect(registeredModel.GetId()).To(Equal("2"))
			Expect(modelVersion.GetId()).To(Equal("3"))
			Expect(modelArtifact.GetId()).To(Equal("1"))

			_, err := utils.Run(exec.Command("kubectl", "apply", "-f", ServingRuntimePath1))
			Expect(err).ToNot(HaveOccurred())
		})

		AfterEach(func() {
			By("removing finalizers from inference service")
			_, err := utils.Run(exec.Command("kubectl", "patch", "inferenceservice", "dummy-inference-service", "--type", "json", "--patch", "[ { \"op\": \"remove\", \"path\": \"/metadata/finalizers\" } ]"))
			Expect(err).ToNot(HaveOccurred())
		})

		It("the controller should create InferenceService with specific model version in model registry", func() {
			_, err := utils.Run(exec.Command("kubectl", "apply", "-f", InferenceServiceWithModelVersionPath))
			Expect(err).ToNot(HaveOccurred())

			var is *openapi.InferenceService
			// incremental id
			isId := "4"
			Eventually(func() error {
				is, err = modelRegistryClient.GetInferenceServiceById(isId)
				return err
			}, timeout, interval).ShouldNot(HaveOccurred())

			Expect(strings.HasPrefix(*is.Name, inferenceServiceName)).To(BeTrue())
			Expect(is.ServingEnvironmentId).To(Equal(*servingEnvironment.Id))
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

			Expect(actualISVC.Labels[constants.ModelRegistryRegisteredModelIdLabel]).To(Equal(""))
			Expect(actualISVC.Labels[constants.ModelRegistryModelVersionIdLabel]).To(Equal(""))
			Expect(actualISVC.Finalizers[0]).To(Equal("modelregistry.opendatahub.io/finalizer"))
		})

		It("the controller should create InferenceService without specific model version in model registry", func() {
			_, err := utils.Run(exec.Command("kubectl", "apply", "-f", InferenceServiceWithoutModelVersionPath))
			Expect(err).ToNot(HaveOccurred())

			var is *openapi.InferenceService
			// incremental id
			isId := "4"
			Eventually(func() error {
				is, err = modelRegistryClient.GetInferenceServiceById(isId)
				return err
			}, timeout, interval).ShouldNot(HaveOccurred())

			Expect(strings.HasPrefix(*is.Name, inferenceServiceName)).To(BeTrue())
			Expect(is.ServingEnvironmentId).To(Equal(*servingEnvironment.Id))
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

			Expect(actualISVC.Labels[constants.ModelRegistryRegisteredModelIdLabel]).To(Equal(""))
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
			Expect(servingEnvironment.GetId()).To(Equal("1"))
			Expect(registeredModel.GetId()).To(Equal("2"))
			Expect(modelVersion.GetId()).To(Equal("3"))
			Expect(modelArtifact.GetId()).To(Equal("1"))

			inferenceService, err = modelRegistryClient.UpsertInferenceService(&openapi.InferenceService{
				Name:                 &versionName,
				DesiredState:         openapi.INFERENCESERVICESTATE_DEPLOYED.Ptr(),
				RegisteredModelId:    *registeredModel.Id,
				ServingEnvironmentId: *servingEnvironment.Id,
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(inferenceService.GetId()).To(Equal("4"))

			_, err := utils.Run(exec.Command("kubectl", "apply", "-f", ServingRuntimePath1))
			Expect(err).ToNot(HaveOccurred())

			_, err = utils.Run(exec.Command("kubectl", "apply", "-f", InferenceServiceWithInfServiceIdPath))
			Expect(err).ToNot(HaveOccurred())
		})

		It("the controller should set the InferenceService desired state to UNDEPLOYED", func() {
			_, err := utils.Run(exec.Command("kubectl", "delete", "-f", InferenceServiceWithInfServiceIdPath))
			Expect(err).ToNot(HaveOccurred())

			var is *openapi.InferenceService
			// incremental id
			isId := "4"
			Eventually(func() bool {
				is, err = modelRegistryClient.GetInferenceServiceById(isId)
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
func deployAndCheckModelRegistry() api.ModelRegistryApi {
	cmd := exec.Command("kubectl", "apply", "-f", ModelRegistryDatabaseDeploymentPath)
	_, err := utils.Run(cmd)
	Expect(err).ToNot(HaveOccurred())

	cmd = exec.Command("kubectl", "apply", "-f", ModelRegistryDeploymentPath)
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
	Expect(len(mrServiceList.Items)).To(Equal(1))

	var grpcPort *int32
	for _, port := range mrServiceList.Items[0].Spec.Ports {
		if port.Name == "grpc-api" {
			grpcPort = &port.NodePort
			break
		}
	}
	Expect(grpcPort).ToNot(BeNil())

	mlmdAddr = fmt.Sprintf("localhost:%d", *grpcPort)
	grpcConn, err := grpc.DialContext(
		ctx,
		mlmdAddr,
		grpc.WithReturnConnectionError(),
		grpc.WithBlock(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	Expect(err).ToNot(HaveOccurred())
	mr, err := core.NewModelRegistryService(grpcConn)
	Expect(err).ToNot(HaveOccurred())

	return mr
}

// undeployModelRegistry cleanup model registry deployments
func undeployModelRegistry() {
	cmd := exec.Command("kubectl", "delete", "-f", ModelRegistryDeploymentPath)
	_, err = utils.Run(cmd)
	Expect(err).ToNot(HaveOccurred())

	cmd = exec.Command("kubectl", "delete", "-f", ModelRegistryDatabaseDeploymentPath)
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

func fillModelRegistryContent(mr api.ModelRegistryApi) {
	envName := WorkingNamespace
	servingEnvironment, err = mr.GetServingEnvironmentByParams(&envName, nil)
	if err != nil {
		// register a new model
		servingEnvironment, err = mr.UpsertServingEnvironment(&openapi.ServingEnvironment{
			Name: &envName,
		})
		Expect(err).ToNot(HaveOccurred())
	}

	registeredModel, err = mr.GetRegisteredModelByParams(&modelName, nil)
	if err != nil {
		// register a new model
		registeredModel, err = mr.UpsertRegisteredModel(&openapi.RegisteredModel{
			Name: &modelName,
		})
		Expect(err).ToNot(HaveOccurred())
	}

	modelVersion, err = mr.GetModelVersionByParams(&versionName, registeredModel.Id, nil)
	if err != nil {
		modelVersion, err = mr.UpsertModelVersion(&openapi.ModelVersion{
			Name: &versionName,
		}, registeredModel.Id)
		Expect(err).ToNot(HaveOccurred())
	}

	modelArtifactName := fmt.Sprintf("%s-artifact", versionName)
	modelArtifact, err = mr.GetModelArtifactByParams(&modelArtifactName, modelVersion.Id, nil)
	if err != nil {
		modelArtifact, err = mr.UpsertModelArtifact(&openapi.ModelArtifact{
			Name:               &modelArtifactName,
			ModelFormatName:    &modelFormatName,
			ModelFormatVersion: &modelFormatVersion,
			StorageKey:         &storageKey,
			StoragePath:        &storagePath,
		}, modelVersion.Id)
		Expect(err).ToNot(HaveOccurred())
	}
}
