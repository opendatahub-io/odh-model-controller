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

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"os"
	"strconv"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	istiov1beta1 "istio.io/client-go/pkg/apis/security/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/go-logr/logr"
	corecontroller "github.com/opendatahub-io/odh-model-controller/internal/controller/core"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/nim"
	servingcontroller "github.com/opendatahub-io/odh-model-controller/internal/controller/serving"
	"github.com/opendatahub-io/odh-model-controller/internal/controller/utils"

	webhookcorev1 "github.com/opendatahub-io/odh-model-controller/internal/webhook/core/v1"
	webhooknimv1 "github.com/opendatahub-io/odh-model-controller/internal/webhook/nim/v1"
	webhookservingv1 "github.com/opendatahub-io/odh-model-controller/internal/webhook/serving/v1"
	webhookservingv1alpha1 "github.com/opendatahub-io/odh-model-controller/internal/webhook/serving/v1alpha1"
	webhookservingv1beta1 "github.com/opendatahub-io/odh-model-controller/internal/webhook/serving/v1beta1"
	// +kubebuilder:scaffold:imports
)

const (
	enableWebhooksEnv = "ENABLE_WEBHOOKS"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utils.RegisterSchemes(scheme)
}

func getEnvAsBool(name string, defaultValue bool) bool {
	valStr := os.Getenv(name)
	if val, err := strconv.ParseBool(valStr); err == nil {
		return val
	}
	return defaultValue
}

// nolint:gocyclo
func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var tlsOpts []func(*tls.Config)
	var monitoringNS string
	var enableMRInferenceServiceReconcile bool

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", false, // TODO: restore to true by default.
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.StringVar(&monitoringNS, "monitoring-namespace", "",
		"The Namespace where the monitoring stack's Prometheus resides.")
	flag.BoolVar(&enableMRInferenceServiceReconcile, "model-registry-inference-reconcile", false,
		"Enable model registry inference service reconciliation. ")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	// Create the manager
	var err error
	cfg := ctrl.GetConfigOrDie()
	mgr := createManager(cfg, metricsAddr, probeAddr, enableLeaderElection, secureMetrics, tlsOpts)

	kubeClient, kubeClientErr := kubernetes.NewForConfig(cfg)
	if kubeClientErr != nil {
		setupLog.Error(kubeClientErr, "unable to create clientset")
		os.Exit(1)
	}

	kserveState := os.Getenv("KSERVE_STATE")
	modelMeshState := os.Getenv("MODELMESH_STATE")
	setupLog.Info("Installation status of Serving Components",
		"kserve-state", kserveState, "modelmesh-state", modelMeshState)

	kserveWithMeshEnabled, kserveWithMeshEnabledErr := utils.VerifyIfComponentIsEnabled(
		context.Background(), mgr.GetClient(), utils.KServeWithServiceMeshComponent)
	if kserveWithMeshEnabledErr != nil {
		setupLog.Error(kserveWithMeshEnabledErr, "could not determine if kserve have service mesh enabled")
	}

	if err := setupReconcilers(
		mgr,
		setupLog,
		kubeClient,
		cfg,
		kserveWithMeshEnabled,
		kserveState, modelMeshState); err != nil {
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder
	if err := setupWebhooks(mgr, setupLog, kserveWithMeshEnabled); err != nil {
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	signalHandlerCtx := ctrl.SetupSignalHandler()
	setupNim(mgr, signalHandlerCtx, kubeClient)

	setupLog.Info("starting manager")
	if err = mgr.Start(signalHandlerCtx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func setupNim(mgr manager.Manager, signalHandlerCtx context.Context, kubeClient *kubernetes.Clientset) {
	var err error

	nimState := os.Getenv("NIM_STATE")
	if nimState == "" {
		nimState = "managed"
	}
	if nimState != "removed" {
		if err = (&nim.AccountReconciler{
			Client:  mgr.GetClient(),
			Scheme:  mgr.GetScheme(),
			KClient: kubeClient,
		}).SetupWithManager(mgr, signalHandlerCtx); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "NIMAccount")
			os.Exit(1)
		}
	} else {
		if err = mgr.Add(&utils.NIMCleanupRunner{Client: mgr.GetClient(), Logger: setupLog}); err != nil {
			setupLog.Error(err, "failed to add NIM cleanup runner")
		}
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(signalHandlerCtx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func createManager(cfg *rest.Config, metricsAddr, probeAddr string,
	enableLeaderElection, secureMetrics bool, tlsOpts []func(*tls.Config)) ctrl.Manager {
	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: tlsOpts,
	})

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.1/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.1/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization

		// TODO(user): If CertDir, CertName, and KeyName are not specified, controller-runtime will automatically
		// generate self-signed certificates for the metrics server. While convenient for development and testing,
		// this setup is not recommended for production.
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "odh-model-controller.opendatahub.io",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
		Client: client.Options{
			Cache: &client.CacheOptions{
				DisableFor: []client.Object{&istiov1beta1.AuthorizationPolicy{}},
			},
		},
		Cache: cache.Options{
			ByObject: map[client.Object]cache.ByObject{
				&corev1.Secret{}: {
					Label: labels.SelectorFromSet(labels.Set{
						"opendatahub.io/managed": "true",
					}),
				},
				&corev1.Pod{}: {
					Label: labels.SelectorFromSet(labels.Set{
						"component": "predictor",
					}),
				},
			},
		},
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}
	return mgr
}

func setupWebhooks(mgr ctrl.Manager, setupLog logr.Logger, kserveWithMeshEnabled bool) error {
	if os.Getenv(enableWebhooksEnv) == "false" {
		setupLog.Info("Webhooks are disabled via environment variable.")
		return nil
	}

	webhookSetups := []struct {
		name    string
		setupFn func(ctrl.Manager) error
	}{
		{"Pod", webhookcorev1.SetupPodWebhookWithManager},
		{"NIMAccount", webhooknimv1.SetupAccountWebhookWithManager},
		{"InferenceService", webhookservingv1beta1.SetupInferenceServiceWebhookWithManager},
		{"InferenceGraph", webhookservingv1alpha1.SetupInferenceGraphWebhookWithManager},
	}

	for _, webhook := range webhookSetups {
		if err := webhook.setupFn(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", webhook.name)
			return err
		}
	}

	// Handle Knative Service webhook setup conditionally
	if kserveWithMeshEnabled {
		if err := webhookservingv1.SetupServiceWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "Knative Service")
			return err
		}
	} else {
		setupLog.Info("Skipping setup of Knative Service validating/mutating Webhook, " +
			"because KServe Serverless setup seems to be disabled.")
	}

	return nil
}

func setupReconcilers(mgr ctrl.Manager, setupLog logr.Logger,
	kubeClient kubernetes.Interface, cfg *rest.Config, kserveWithMeshEnabled bool, kserveState string, _ string) error {
	if err := setupInferenceServiceReconciler(mgr, kubeClient, cfg); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "InferenceService")
		return err
	}
	if err := setupSecretReconciler(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Secret")
		return err
	}
	if err := setupConfigMapReconciler(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ConfigMap")
		return err
	}
	if err := setupPodReconciler(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Pod")
		return err
	}
	if err := setupServingRuntimeReconciler(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ServingRuntime")
		return err
	}

	if kserveState == "managed" {
		if err := setupInferenceGraphReconciler(mgr, kserveWithMeshEnabled); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "InferenceGraph")
			return err
		}
	} else {
		setupLog.Info("kserve state is not managed, skipping controller", "controller", "InferenceGraph")
	}

	return nil
}

func setupInferenceServiceReconciler(mgr ctrl.Manager, kubeClient kubernetes.Interface, cfg *rest.Config) error {
	return (servingcontroller.NewInferenceServiceReconciler(
		setupLog,
		mgr.GetClient(),
		mgr.GetScheme(),
		mgr.GetAPIReader(),
		kubeClient,
		getEnvAsBool("MESH_DISABLED", false),
		getEnvAsBool("ENABLE_MR_INFERENCE_SERVICE_RECONCILE", false),
		getEnvAsBool("MR_SKIP_TLS_VERIFY", false),
		cfg.BearerToken,
	)).SetupWithManager(mgr)
}

func setupSecretReconciler(mgr ctrl.Manager) error {
	return (&corecontroller.SecretReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr)
}

func setupConfigMapReconciler(mgr ctrl.Manager) error {
	return (&corecontroller.ConfigMapReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr)
}

func setupPodReconciler(mgr ctrl.Manager) error {
	return (&corecontroller.PodReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr)
}

func setupServingRuntimeReconciler(mgr ctrl.Manager) error {
	return (&servingcontroller.ServingRuntimeReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr)
}

func setupInferenceGraphReconciler(mgr ctrl.Manager, isServerlessMode bool) error {
	return servingcontroller.NewInferenceGraphReconciler(mgr).SetupWithManager(mgr, isServerlessMode)
}
