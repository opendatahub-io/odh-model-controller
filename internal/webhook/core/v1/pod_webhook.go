/*
Copyright 2024.

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

package v1

import (
	"context"
	"fmt"
	"os"

	"github.com/go-logr/logr"
	kserveconstants "github.com/kserve/kserve/pkg/constants"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// +kubebuilder:webhook:path=/mutate--v1-pod,mutating=true,failurePolicy=fail,groups="",resources=pods,verbs=create,versions=v1,name=mutating.pod.odh-model-controller.opendatahub.io,admissionReviewVersions=v1,sideEffects=none

var podlog logr.Logger

// Mutator is a webhook that injects incoming pods
type PodMutatorDefaultor struct {
	client client.Client
}

var _ webhook.CustomDefaulter = &PodMutatorDefaultor{}

// Handle incoming Pod and executes mutation logic.
func (m *PodMutatorDefaultor) podMutator(pod *corev1.Pod) error {

	if err := m.mutate(pod); err != nil {
		podlog.Error(err, "Failed to mutate pod", "name", pod.Name, "namespace", pod.Namespace)
		return err
	}

	return nil
}

func (m *PodMutatorDefaultor) mutate(pod *corev1.Pod) error {
	if needToAddRayTLSGenerator(pod) {
		addRayTLSVolumesAndVolumeMounts(pod)
		rayTLSGeneratorScript, err := getRayTLSGeneratorScriptInitContainer(m.client)
		if err != nil {
			return err
		}
		pod.Spec.InitContainers = append(pod.Spec.InitContainers, *rayTLSGeneratorScript)
	}
	return nil
}

// Add ray tls volumes into the pod.
// Add ray tls volumeMount into the pod when the container name is "kserve-container" or "worker-container".
func addRayTLSVolumesAndVolumeMounts(pod *corev1.Pod) {
	pod.Spec.Volumes = append(pod.Spec.Volumes, []corev1.Volume{
		{
			Name: constants.RayTLSVolumeName,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		{
			Name: constants.RayTLSSecretVolumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: constants.RayTLSSecretName,
				},
			},
		},
	}...)

	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]

		if container.Name == "kserve-container" || container.Name == "worker-container" {
			container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
				Name:      constants.RayTLSVolumeName,
				MountPath: constants.RayTLSVolumeMountPath,
			})
			break
		}
	}
}

// Add ray tls generator init-container into the pod when the pod has pod environment variable "RAY_USE_TLS" is 1.
func getRayTLSGeneratorScriptInitContainer(client client.Client) (*corev1.Container, error) {

	ray_cert_image, err := getRayCertGeneratorImage(client)
	if err != nil {
		return nil, err
	}

	script := `
SECRET_DIR="/etc/ray-secret"
TLS_DIR="/etc/ray/tls"
SOURCE_RAY_TLS_PEM_FILE_PATH="${SECRET_DIR}/${POD_IP}"
TARGET_RAY_TLS_PEM_FILE_PATH="${TLS_DIR}/tls.pem"
SOURCE_RAY_CA_PEM_FILE_PATH="${SECRET_DIR}/ca.crt"
TARGET_RAY_CA_PEM_FILE_PATH="${TLS_DIR}/ca.crt"

RETRY_INTERVAL=10
MAX_RETRIES=24
INITIAL_DELAY_SECONDS=10

sleep $INITIAL_DELAY_SECONDS

for ((retries = 0; retries <= MAX_RETRIES; retries++)); do
    if [[ -f "$SOURCE_RAY_CA_PEM_FILE_PATH" ]]; then
        cp "$SOURCE_RAY_CA_PEM_FILE_PATH" "$TARGET_RAY_CA_PEM_FILE_PATH"
        chmod 644 "$TARGET_RAY_CA_PEM_FILE_PATH"
        break
    fi
    echo "Not found ca cert. Retrying in ${RETRY_INTERVAL} seconds... ($retries/$MAX_RETRIES)"
    sleep $RETRY_INTERVAL
done

if [[ ! -f "$SOURCE_RAY_CA_PEM_FILE_PATH" ]]; then
    echo "Error: CA Cert file not found!"
fi

for ((retires = 0; retries <= MAX_RETRIES; retries++)); do
    if [[ -f "$SOURCE_RAY_TLS_PEM_FILE_PATH" ]]; then
        cp "$SOURCE_RAY_TLS_PEM_FILE_PATH" "$TARGET_RAY_TLS_PEM_FILE_PATH"
        chmod 644 "$TARGET_RAY_TLS_PEM_FILE_PATH"
        break
    fi
    echo "Cert file not generated yet. Retrying in ${RETRY_INTERVAL} seconds... ($retries/$MAX_RETRIES)"

    sleep $RETRY_INTERVAL
done

if [[ ! -f "$SOURCE_RAY_TLS_PEM_FILE_PATH" ]]; then
    echo "Error: Cert file not generated!"
fi

if [ -f "$TARGET_RAY_TLS_PEM_FILE_PATH" ] && [ -f "$TARGET_RAY_CA_PEM_FILE_PATH" ]; then
    echo "Certificate files created successfully!"
    exit 0
else
    echo "Error: Failed to create certificate files."
    exit 1
fi

`

	return &corev1.Container{
		Name:  constants.RayTLSGeneratorInitContainerName,
		Image: ray_cert_image,
		Command: []string{
			"sh",
			"-c",
			script,
		},
		TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
		Env: []corev1.EnvVar{
			{
				Name: "POD_IP",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "status.podIP",
					},
				},
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      constants.RayTLSVolumeName,
				MountPath: constants.RayTLSVolumeMountPath,
			},
			{
				Name:      constants.RayTLSSecretVolumeName,
				MountPath: constants.RayTLSSecretMountPath,
			},
		},
	}, nil
}

// Fetch the value of ray-cert-generator-image from the ConfigMap
func getRayCertGeneratorImage(client client.Client) (string, error) {
	controllerNs := os.Getenv("POD_NAMESPACE")

	configMap := &corev1.ConfigMap{}
	err := client.Get(context.TODO(), types.NamespacedName{
		Namespace: controllerNs,
		Name:      "odh-model-controller-parameters",
	}, configMap)
	if err != nil {
		return "", err
	}

	image, ok := configMap.Data["ray-tls-generator-image"]
	if !ok {
		return "", fmt.Errorf("ray-tls-generator-image key not found in ConfigMap")
	}

	return image, nil
}

func needToAddRayTLSGenerator(pod *corev1.Pod) bool {
	// Check if the pod already has ray tls generator init container.
	for _, initContainer := range pod.Spec.InitContainers {
		if initContainer.Name == constants.RayTLSGeneratorInitContainerName {
			return false
		}
	}

	// Check if RAY_USE_TLS is set to 1 in the main containers
	for _, container := range pod.Spec.Containers {
		if container.Name == kserveconstants.InferenceServiceContainerName || container.Name == constants.WorkerContainerName {
			for _, envVar := range container.Env {
				if envVar.Name == constants.RayUseTlsEnvName && envVar.Value != "0" {
					return true
				}
			}
		}
	}
	return false
}

func (m *PodMutatorDefaultor) Default(ctx context.Context, obj runtime.Object) error {
	pod, ok := obj.(*corev1.Pod)

	if !ok {
		return fmt.Errorf("expected an Pod object but got %T", obj)
	}
	logger := podlog.WithValues("name", pod.GetName())
	logger.Info("Defaulting for Pod")

	err := m.podMutator(pod)
	if err != nil {
		return err
	}

	return nil
}

// SetupPodWebhookWithManager sets up the MutatingWebhook with the controller manager.
func SetupPodWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(&corev1.Pod{}).
		WithDefaulter(&PodMutatorDefaultor{client: mgr.GetClient()}).
		Complete()
}
