package core

import (
	"context"
	"sync"
	"testing"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/google/uuid"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/constants"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func setupLogger(t *testing.T) logr.Logger {
	zapLogger, _ := zap.NewProduction()
	t.Cleanup(func() {
		_ = zapLogger.Sync()
	})
	return zapr.NewLogger(zapLogger)
}

// TestUpdateSecret_Concurrent tests that updateSecret correctly updates secret data with optimistic locking.
// Used TEST CERT
func TestCheckMultiNodePod(t *testing.T) {
	tests := []struct {
		name     string
		pod      *corev1.Pod
		expected bool
	}{
		{
			name: "RAY_USE_TLS as first env var should return true",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "kserve-container",
							Env: []corev1.EnvVar{
								{Name: constants.RayUseTlsEnvName, Value: "1"},
								{Name: "OTHER_VAR", Value: "other"},
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "RAY_USE_TLS NOT as first env var should return true (regression test)",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "kserve-container",
							Env: []corev1.EnvVar{
								{Name: "PATH", Value: "/usr/bin"},
								{Name: "HOME", Value: "/root"},
								{Name: "OTHER_VAR", Value: "value"},
								{Name: constants.RayUseTlsEnvName, Value: "1"},
								{Name: "ANOTHER_VAR", Value: "another"},
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "RAY_USE_TLS with value 0 should return false",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "kserve-container",
							Env: []corev1.EnvVar{
								{Name: constants.RayUseTlsEnvName, Value: "0"},
							},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "RAY_USE_TLS with empty value should return true",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "kserve-container",
							Env: []corev1.EnvVar{
								{Name: constants.RayUseTlsEnvName, Value: ""},
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "No RAY_USE_TLS env var should return false",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "kserve-container",
							Env: []corev1.EnvVar{
								{Name: "PATH", Value: "/usr/bin"},
								{Name: "HOME", Value: "/root"},
							},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "RAY_USE_TLS in worker-container should return true",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "worker-container",
							Env: []corev1.EnvVar{
								{Name: "OTHER_VAR", Value: "other"},
								{Name: constants.RayUseTlsEnvName, Value: "1"},
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "RAY_USE_TLS in second container should return true",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "init-container",
							Env: []corev1.EnvVar{
								{Name: "SOME_VAR", Value: "value"},
							},
						},
						{
							Name: "kserve-container",
							Env: []corev1.EnvVar{
								{Name: "PATH", Value: "/usr/bin"},
								{Name: constants.RayUseTlsEnvName, Value: "1"},
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "RAY_USE_TLS in different container name should return false",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "some-other-container",
							Env: []corev1.EnvVar{
								{Name: constants.RayUseTlsEnvName, Value: "1"},
							},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "Empty pod with no containers should return false",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := checkMultiNodePod(tt.pod)
			assert.Equal(t, tt.expected, result, "checkMultiNodePod returned unexpected result")
		})
	}
}

func TestCheckPodHasIP(t *testing.T) {
	tests := []struct {
		name     string
		oldPod   *corev1.Pod
		newPod   *corev1.Pod
		expected bool
	}{
		{
			name:   "New pod with IP and no old pod should return true",
			oldPod: nil,
			newPod: &corev1.Pod{
				Status: corev1.PodStatus{
					PodIP: "192.168.1.1",
				},
			},
			expected: true,
		},
		{
			name:   "New pod without IP and no old pod should return false",
			oldPod: nil,
			newPod: &corev1.Pod{
				Status: corev1.PodStatus{
					PodIP: "",
				},
			},
			expected: false,
		},
		{
			name: "Pod IP changed should return true",
			oldPod: &corev1.Pod{
				Status: corev1.PodStatus{
					PodIP: "192.168.1.1",
				},
			},
			newPod: &corev1.Pod{
				Status: corev1.PodStatus{
					PodIP: "192.168.1.2",
				},
			},
			expected: true,
		},
		{
			name: "Pod IP unchanged should return false",
			oldPod: &corev1.Pod{
				Status: corev1.PodStatus{
					PodIP: "192.168.1.1",
				},
			},
			newPod: &corev1.Pod{
				Status: corev1.PodStatus{
					PodIP: "192.168.1.1",
				},
			},
			expected: false,
		},
		{
			name: "Old pod had IP, new pod has no IP should return true",
			oldPod: &corev1.Pod{
				Status: corev1.PodStatus{
					PodIP: "192.168.1.1",
				},
			},
			newPod: &corev1.Pod{
				Status: corev1.PodStatus{
					PodIP: "",
				},
			},
			expected: true,
		},
		{
			name: "Old pod had no IP, new pod has IP should return true",
			oldPod: &corev1.Pod{
				Status: corev1.PodStatus{
					PodIP: "",
				},
			},
			newPod: &corev1.Pod{
				Status: corev1.PodStatus{
					PodIP: "192.168.1.1",
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := checkPodHasIP(tt.oldPod, tt.newPod)
			assert.Equal(t, tt.expected, result, "checkPodHasIP returned unexpected result")
		})
	}
}

func TestUpdateSecret_Concurrent(t *testing.T) {
	namespace := "test-namespace"
	secretName := constants.RayTLSSecretName
	logger := setupLogger(t)

	fakeClient := clientfake.NewClientBuilder().WithObjects(
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:            secretName,
				Namespace:       namespace,
				ResourceVersion: "1",
			},
			Data: map[string][]byte{
				"ca.crt": []byte("-----BEGIN CERTIFICATE-----\nMIIFWTCCA0GgAwIBAgIIEz4ewFGT1pQwDQYJKoZIhvcNAQELBQAwQDEgMB4GA1UE\nChMXb3BlbmRhdGFodWItc2VsZi1zaWduZWQxHDAaBgNVBAMTE3NlbGYtc2lnbmVk\nLWNhLWNlcnQwHhcNMjUwMTI4MTQ1OTU5WhcNMzAwMTI3MTQ1OTU5WjBAMSAwHgYD\nVQQKExdvcGVuZGF0YWh1Yi1zZWxmLXNpZ25lZDEcMBoGA1UEAxMTc2VsZi1zaWdu\nZWQtY2EtY2VydDCCAiIwDQYJKoZIhvcNAQEBBQADggIPADCCAgoCggIBANBpui1r\nUv4kwHUhYGv5VJVo0uwXjI8yCzT2xmCdrtBaIN16HJa4m0p4yd6qsazkCquxr2jn\nI58IfqqbHqMGzXnYMgvyt+NfTzOx/LIXKf13kydrpTwMTAszbo6dAf6h5YEPRTTG\noeVzS+DCKS/M1b00nkxT5NWCzHXqFmN+vZNbmGhi8hQStLKvGmkfwi55xHjrBzrf\nuQD3Sd0YmKjY5fSSNYgwVVpCzgxzliJJHBvCxZii0aK2ssAhProoxOF1wzVXr/ip\nt3QVyUTlCe1BmKQ7xSuFH0r1BmCJei7ArbPFA0/n5FKv8PocVyeqFcVE315Cfn44\nugLKYX9TU3r6g50CrY9d9m6rH/91ZH3xnQt2K5jRPYLdV2nBfSadv3oBBPqt/bPq\nAdzjhHLPmpbOMMTNd0DPY+Yu0Ujrq3pY/XMSzRAD/rFpxiDaA3awort1JLviancP\nJfOnm0XhGK4uZ8ZYHla24PT0wO8rE+IyY2EqptGRn/l4ES4wAJPbb8xfbYDjte6G\nI3In7A4hGp/PjeAF+mcLpVXzscnwMtmWBapiZszmn3pV5dNx2wo8ESRX02Y/z75g\nXp+2j86B46yXAGMlrd1rKr3nFZptO9YVUQzPbnnFxcZZDQlXMPrOmkRnnlZwStKe\nsaz1iYzHJuhi2q8Y+lkoyOGhvs305O116bdJAgMBAAGjVzBVMA4GA1UdDwEB/wQE\nAwICpDATBgNVHSUEDDAKBggrBgEFBQcDATAPBgNVHRMBAf8EBTADAQH/MB0GA1Ud\nDgQWBBRFLzhGI/qyzL7Vr6zYfVWRywT76DANBgkqhkiG9w0BAQsFAAOCAgEAqqPk\nEvBKH9X6wvYdFVSo3lPtN+nG72PrzguWnnNpwFIDLSwDPiTT2RzjE0MjeRK31l16\nqSQyRTpcTdNGcvwCWdAg3JMas/GZX8q8Q7CeHFuzKgREgZY0aRmdHSV3ogsMCqOh\nqid+TpwESFILiUhE+oLEod0OLJ8WDptCbaKj6rU12Kmn2KWt5SlxtO8C06J7YY77\nAzvgIrn+TNrD/aynnzkDgvt+0y71CkQKgZgVlTlB6mv9So9ANJ4g3BR52eSoEdcZ\nIm2rphaEdBPMcWuF9paBz4SzJvRqpE0NORCCE0Ffzk1ThR1CB0bIzeW6mE2Ljznc\nRYXsDcLl3r0E2+dY0y/ZueI8pwm0ARTo5Nb2b51HcGIaHsS6/nwPEP1sX9HSZoIO\nkVAyRzcQ98IgcbvgHqgZ96ZQ33lAugw+rfLWHbxLYNb8xoF8B4/1QZiOcgvD9Qa2\nJeI4/eEdwiYkagVDz3ctEU1uBDjtjApNGxA7bPMaCDYomSx01YqQNj4wyMPvWYiv\nh1gdtiRzrnoLRzVUp+gd3kL9WxLzxcr/yk1+7RChaomSulNKwu2jhqVHYpyW2Rs6\noeSkjd7y2Scw+l7b1SlAlX28vtHTpAH37U8JGxBPBk6jvstOCYhRIoZkSr9xRV7b\n7WCXq0HEwPv+6n338L50A7jQz0o4YQUVUiSSrwY=\n-----END CERTIFICATE-----\n"),
			},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:            constants.RayCASecretName,
				Namespace:       WorkingNamespace,
				ResourceVersion: "1",
			},
			Data: map[string][]byte{
				corev1.TLSCertKey:       []byte("-----BEGIN CERTIFICATE-----\nMIIFWTCCA0GgAwIBAgIIEz4ewFGT1pQwDQYJKoZIhvcNAQELBQAwQDEgMB4GA1UE\nChMXb3BlbmRhdGFodWItc2VsZi1zaWduZWQxHDAaBgNVBAMTE3NlbGYtc2lnbmVk\nLWNhLWNlcnQwHhcNMjUwMTI4MTQ1OTU5WhcNMzAwMTI3MTQ1OTU5WjBAMSAwHgYD\nVQQKExdvcGVuZGF0YWh1Yi1zZWxmLXNpZ25lZDEcMBoGA1UEAxMTc2VsZi1zaWdu\nZWQtY2EtY2VydDCCAiIwDQYJKoZIhvcNAQEBBQADggIPADCCAgoCggIBANBpui1r\nUv4kwHUhYGv5VJVo0uwXjI8yCzT2xmCdrtBaIN16HJa4m0p4yd6qsazkCquxr2jn\nI58IfqqbHqMGzXnYMgvyt+NfTzOx/LIXKf13kydrpTwMTAszbo6dAf6h5YEPRTTG\noeVzS+DCKS/M1b00nkxT5NWCzHXqFmN+vZNbmGhi8hQStLKvGmkfwi55xHjrBzrf\nuQD3Sd0YmKjY5fSSNYgwVVpCzgxzliJJHBvCxZii0aK2ssAhProoxOF1wzVXr/ip\nt3QVyUTlCe1BmKQ7xSuFH0r1BmCJei7ArbPFA0/n5FKv8PocVyeqFcVE315Cfn44\nugLKYX9TU3r6g50CrY9d9m6rH/91ZH3xnQt2K5jRPYLdV2nBfSadv3oBBPqt/bPq\nAdzjhHLPmpbOMMTNd0DPY+Yu0Ujrq3pY/XMSzRAD/rFpxiDaA3awort1JLviancP\nJfOnm0XhGK4uZ8ZYHla24PT0wO8rE+IyY2EqptGRn/l4ES4wAJPbb8xfbYDjte6G\nI3In7A4hGp/PjeAF+mcLpVXzscnwMtmWBapiZszmn3pV5dNx2wo8ESRX02Y/z75g\nXp+2j86B46yXAGMlrd1rKr3nFZptO9YVUQzPbnnFxcZZDQlXMPrOmkRnnlZwStKe\nsaz1iYzHJuhi2q8Y+lkoyOGhvs305O116bdJAgMBAAGjVzBVMA4GA1UdDwEB/wQE\nAwICpDATBgNVHSUEDDAKBggrBgEFBQcDATAPBgNVHRMBAf8EBTADAQH/MB0GA1Ud\nDgQWBBRFLzhGI/qyzL7Vr6zYfVWRywT76DANBgkqhkiG9w0BAQsFAAOCAgEAqqPk\nEvBKH9X6wvYdFVSo3lPtN+nG72PrzguWnnNpwFIDLSwDPiTT2RzjE0MjeRK31l16\nqSQyRTpcTdNGcvwCWdAg3JMas/GZX8q8Q7CeHFuzKgREgZY0aRmdHSV3ogsMCqOh\nqid+TpwESFILiUhE+oLEod0OLJ8WDptCbaKj6rU12Kmn2KWt5SlxtO8C06J7YY77\nAzvgIrn+TNrD/aynnzkDgvt+0y71CkQKgZgVlTlB6mv9So9ANJ4g3BR52eSoEdcZ\nIm2rphaEdBPMcWuF9paBz4SzJvRqpE0NORCCE0Ffzk1ThR1CB0bIzeW6mE2Ljznc\nRYXsDcLl3r0E2+dY0y/ZueI8pwm0ARTo5Nb2b51HcGIaHsS6/nwPEP1sX9HSZoIO\nkVAyRzcQ98IgcbvgHqgZ96ZQ33lAugw+rfLWHbxLYNb8xoF8B4/1QZiOcgvD9Qa2\nJeI4/eEdwiYkagVDz3ctEU1uBDjtjApNGxA7bPMaCDYomSx01YqQNj4wyMPvWYiv\nh1gdtiRzrnoLRzVUp+gd3kL9WxLzxcr/yk1+7RChaomSulNKwu2jhqVHYpyW2Rs6\noeSkjd7y2Scw+l7b1SlAlX28vtHTpAH37U8JGxBPBk6jvstOCYhRIoZkSr9xRV7b\n7WCXq0HEwPv+6n338L50A7jQz0o4YQUVUiSSrwY=\n-----END CERTIFICATE-----\n"),
				corev1.TLSPrivateKeyKey: []byte("-----BEGIN PRIVATE KEY-----\n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\n-----END PRIVATE KEY-----\n"),
			},
		},
	).Build()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "example-pod",
			Namespace: namespace,
			Labels: map[string]string{
				"component": "predictor",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "kserve-container",
					Image: "nginx:latest",
					Args: []string{
						"--arg1=value1",
						"--arg2=value2",
					},
					Env: []corev1.EnvVar{
						{
							Name:  constants.RayUseTlsEnvName,
							Value: "1",
						},
					},
				},
			},
		},
	}
	podIPs := []string{"192.168.1.1", "192.168.1.2", "192.168.1.3"}

	caCertSecret := &corev1.Secret{}
	err := fakeClient.Get(context.TODO(), client.ObjectKey{Namespace: WorkingNamespace, Name: constants.RayCASecretName}, caCertSecret)
	assert.NoError(t, err, "should be able to fetch the updated Secret")

	rayCertSecret := &corev1.Secret{}
	err = fakeClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: secretName}, rayCertSecret)
	assert.NoError(t, err, "should be able to fetch the updated Secret")

	// Create 3 pods simultaneously
	var wg sync.WaitGroup
	for i := range podIPs {
		wg.Add(1)
		go func(podIP string) {
			defer wg.Done()
			podCopy := pod.DeepCopy()
			podCopy.Name = "pod-" + uuid.New().String()
			podCopy.Status.PodIP = podIP
			err = fakeClient.Create(context.TODO(), podCopy)
			assert.NoError(t, err, "should be able to create the Pod")

			err := updateSecret(context.TODO(), fakeClient, logger, caCertSecret, namespace, podCopy)
			assert.NoError(t, err, "updateSecret should not return an error")
		}(podIPs[i])
	}
	wg.Wait()

	// after 3 requests handled, check all IPs should exist in the ray cert secret
	updatedSecret := &corev1.Secret{}
	err = fakeClient.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: secretName}, updatedSecret)
	assert.NoError(t, err, "should be able to fetch the updated Secret")

	for _, podIP := range podIPs {
		combinedCert, exists := updatedSecret.Data[podIP]

		assert.True(t, exists, "Secret should contain certificate data for pod IP: %s", podIP)
		assert.NotEmpty(t, combinedCert, "Certificate data should not be empty for pod IP: %s", podIP)
	}

}
