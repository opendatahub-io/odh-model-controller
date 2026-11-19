package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
)

func TestRenderedServiceMonitorSelectsServiceAndVerifiesTLS(t *testing.T) {
	repoRoot := filepath.Join("..", "..")

	// Render through the same Make target used by deploy-server, including its
	// namespace replacement. Use two namespaces to catch a hard-coded DNS name.
	for _, namespace := range []string{"redhat-ods-applications", "opendatahub"} {
		t.Run(namespace, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, "make", "-s", "render-server", "NAMESPACE="+namespace)
			cmd.Dir = repoRoot
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, "render model-serving-api manifests: %s", output)

			bundle, err := os.ReadFile(filepath.Join(repoRoot, "bin", "server-overlay", "server-bundle.yaml"))
			require.NoError(t, err)
			assertServiceMonitor(t, bundle, namespace)
		})
	}

	// These overlays also ship the server and apply their own namespace values.
	for _, overlay := range []struct {
		path      string
		namespace string
	}{
		{"config/default", "odh-model-controller-system"},
		{"config/base", "opendatahub"},
	} {
		t.Run(overlay.path, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			kustomize, err := filepath.Abs(filepath.Join(repoRoot, "bin", "kustomize"))
			require.NoError(t, err)
			cmd := exec.CommandContext(ctx, kustomize, "build", overlay.path)
			cmd.Dir = repoRoot
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			bundle, err := cmd.Output()
			require.NoError(t, err, "render %s: %s", overlay.path, stderr.String())
			assertServiceMonitor(t, bundle, overlay.namespace)
		})
	}
}

func assertServiceMonitor(t *testing.T, bundle []byte, namespace string) {
	t.Helper()
	const serviceName = "model-serving-api"
	const monitorName = "model-serving-api-metrics"
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(bundle), 4096)
	var service, monitor *unstructured.Unstructured
	for {
		var object map[string]interface{}
		err := decoder.Decode(&object)
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		resource := &unstructured.Unstructured{Object: object}
		switch {
		case resource.GetKind() == "Service" && resource.GetName() == serviceName:
			service = resource
		case resource.GetKind() == "ServiceMonitor" && resource.GetName() == monitorName:
			monitor = resource
		}
	}
	require.NotNil(t, service)
	require.NotNil(t, monitor)
	require.Equal(t, namespace, service.GetNamespace())
	require.Equal(t, namespace, monitor.GetNamespace())

	selector, found, err := unstructured.NestedStringMap(monitor.Object, "spec", "selector", "matchLabels")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, serviceName, selector["app.kubernetes.io/name"])
	_, found, err = unstructured.NestedSlice(monitor.Object, "spec", "selector", "matchExpressions")
	require.NoError(t, err)
	require.False(t, found)
	for key, value := range selector {
		require.Equal(t, value, service.GetLabels()[key], "ServiceMonitor must select the Service")
	}

	endpoints, found, err := unstructured.NestedSlice(monitor.Object, "spec", "endpoints")
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, endpoints, 1)
	endpoint, ok := endpoints[0].(map[string]interface{})
	require.True(t, ok)
	port, found, err := unstructured.NestedString(endpoint, "port")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "metrics", port)
	scheme, found, err := unstructured.NestedString(endpoint, "scheme")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "https", scheme)
	serverName, found, err := unstructured.NestedString(endpoint, "tlsConfig", "serverName")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, fmt.Sprintf("%s.%s.svc", serviceName, namespace), serverName)
	caName, found, err := unstructured.NestedString(endpoint, "tlsConfig", "ca", "configMap", "name")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "openshift-service-ca.crt", caName)
	caKey, found, err := unstructured.NestedString(endpoint, "tlsConfig", "ca", "configMap", "key")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "service-ca.crt", caKey)
	_, found, err = unstructured.NestedBool(endpoint, "tlsConfig", "insecureSkipVerify")
	require.NoError(t, err)
	require.False(t, found, "TLS verification must remain enabled")
}
