package core

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// +kubebuilder:webhook:path=/mutate--v1-pod,mutating=true,failurePolicy=fail,groups="",resources=pods,verbs=create,versions=v1,name=mutating.pod.odh-model-controller.opendatahub.io,reinvocationPolicy=IfNeeded,admissionReviewVersions=v1,sideEffects=none

var podlog logr.Logger

// Mutator is a webhook that injects incoming pods
type PodMutatorDefaultor struct {
	Client client.Client
}

var _ webhook.CustomDefaulter = &PodMutatorDefaultor{}

// Handle incoming Pod and executes mutation logic.
func (m *PodMutatorDefaultor) podMutator(pod *corev1.Pod) error {
	if !needMutate(pod) {
		return nil
	}

	if err := m.mutate(pod); err != nil {
		podlog.Error(err, "Failed to mutate pod", "name", pod.Name, "namespace", pod.Namespace)
		return err
	}

	return nil
}

func (m *PodMutatorDefaultor) mutate(pod *corev1.Pod) error {
	if needToAddRayTLSGenerator(pod) {
		rayTLSGeneratorScript := getRayTLSGeneratorScriptInitContainer()
		pod.Spec.InitContainers = append(pod.Spec.InitContainers, *rayTLSGeneratorScript)
	}
	return nil
}

// Add ray tls generator init-container into the pod when the pod has pod environment variable "RAY_USE_TLS" is 1.
func getRayTLSGeneratorScriptInitContainer() *corev1.Container {

	script := `
echo "Generating Self Signed Certificate for Ray nodes"
REFORMAT_IP=$(echo ${POD_IP} | sed 's+\.+\\.+g')
JSONPATH={.data.$REFORMAT_IP}
RAY_PEM_CONTENT=""
RAY_CA_PEM_CONTENT=$(oc get secret ray-tls -n $POD_NAMESPACE -o jsonpath="{.data.ca\.crt}")
TARGET_RAY_CA_PEM_FILE_PATH="/etc/ray/tls/ca.crt"
TARGET_RAY_TLS_PEM_FILE_PATH="/etc/ray/tls/tls.pem"
MAX_RETRIES=15
RETRY_INTERVAL=2
retries=0

while [ $retries -lt $MAX_RETRIES ]; do
  RAY_PEM_CONTENT=$(oc get secret ray-tls -n $POD_NAMESPACE -o jsonpath="$JSONPATH")
  if [[ -n $RAY_PEM_CONTENT ]]; then
    break
  fi
  retries=$((retries + 1))
  echo "Cert file not generated yet. Retrying in ${RETRY_INTERVAL} seconds... ($retries/$MAX_RETRIES)"
  sleep $RETRY_INTERVAL
done
if [ $retries -eq $MAX_RETRIES ]; then
  echo "Error: Cert file not generated!"
  exit 1
fi

echo $RAY_PEM_CONTENT|base64 -d > $TARGET_RAY_TLS_PEM_FILE_PATH
echo $RAY_CA_PEM_CONTENT|base64 -d > $TARGET_RAY_CA_PEM_FILE_PATH

if [ -f "$TARGET_RAY_TLS_PEM_FILE_PATH" ] && [ -f "$TARGET_RAY_CA_PEM_FILE_PATH" ] ; then
  echo "Certificate files created successfully!"
  exit 0
else
  echo "Error: Failed to create certificate files."
  exit 1
fi
`
	return &corev1.Container{
		Name:  constants.RayTLSGeneratorInitContainerName,
		Image: "registry.redhat.io/openshift4/ose-cli@sha256:25fef269ac6e7491cb8340119a9b473acbeb53bc6970ad029fdaae59c3d0ca61",
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
			{
				Name: "POD_NAMESPACE",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "metadata.namespace",
					},
				},
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "ray-tls",
				MountPath: "/etc/ray/tls",
			},
		},
	}
}

func needMutate(pod *corev1.Pod) bool {
	return needToAddRayTLSGenerator(pod)
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
		if container.Name == "kserve-container" || container.Name == "worker-container" {
			for _, envVar := range container.Env {
				if envVar.Name == "RAY_USE_TLS" && envVar.Value == "1" {
					return true
				}
			}
		}
	}
	return false
}

// Default implements webhook.CustomDefaulter so a webhook will be registered for the Kind InferenceGraph.
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
		WithDefaulter(&PodMutatorDefaultor{Client: mgr.GetClient()}).
		Complete()
}
