/*
Copyright 2022.

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
	"flag"
	"github.com/opendatahub-io/odh-model-controller/controllers/webhook"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	knservingv1 "knative.dev/serving/pkg/apis/serving/v1"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"strconv"

	// to ensure that exec-entrypoint and run can make use of them.
	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)

	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"istio.io/client-go/pkg/apis/security/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/opendatahub-io/odh-model-controller/controllers"
	"github.com/opendatahub-io/odh-model-controller/controllers/utils"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() { //nolint:gochecknoinits //reason this way we ensure schemes are always registered before we start anything
	utils.RegisterSchemes(scheme)
}

// ClusterRole permissions

// +kubebuilder:rbac:groups=serving.kserve.io,resources=inferenceservices,verbs=get;list;watch;update
// +kubebuilder:rbac:groups=serving.kserve.io,resources=inferenceservices/finalizers,verbs=get;list;watch;update
// +kubebuilder:rbac:groups=serving.kserve.io,resources=servingruntimes,verbs=get;list;watch;create;update
// +kubebuilder:rbac:groups=serving.kserve.io,resources=servingruntimes/finalizers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.istio.io,resources=virtualservices,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.istio.io,resources=virtualservices/finalizers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.istio.io,resources=gateways,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=security.istio.io,resources=peerauthentications,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=security.istio.io,resources=authorizationpolicies,verbs=get;list
// +kubebuilder:rbac:groups=telemetry.istio.io,resources=telemetries,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=maistra.io,resources=servicemeshmembers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=maistra.io,resources=servicemeshmemberrolls,verbs=get;list;watch
// +kubebuilder:rbac:groups=maistra.io,resources=servicemeshcontrolplanes,verbs=get;list;watch;use
// +kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=route.openshift.io,resources=routes/custom-host,verbs=create
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterrolebindings;rolebindings,verbs=get;list;watch;create;update;patch;watch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors;podmonitors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=extensions,resources=ingresses,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces;pods;endpoints,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;delete;patch
// +kubebuilder:rbac:groups="",resources=secrets;configmaps;serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=authorino.kuadrant.io,resources=authconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=datasciencecluster.opendatahub.io,resources=datascienceclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=dscinitialization.opendatahub.io,resources=dscinitializations,verbs=get;list;watch

func getEnvAsBool(name string, defaultValue bool) bool {
	valStr := os.Getenv(name)
	if val, err := strconv.ParseBool(valStr); err == nil {
		return val
	}
	return defaultValue
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var monitoringNS string
	var probeAddr string
	var enableMRInferenceServiceReconcile bool

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
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

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: server.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "odh-model-controller",
		Client: client.Options{
			Cache: &client.CacheOptions{
				DisableFor: []client.Object{&v1beta1.AuthorizationPolicy{}},
			},
		},
		Cache: cache.Options{
			ByObject: map[client.Object]cache.ByObject{
				&corev1.Secret{}: {
					Label: labels.SelectorFromSet(labels.Set{
						"opendatahub.io/managed": "true",
					}),
				},
			},
		},
	})

	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	//Setup InferenceService controller
	if err = (controllers.NewOpenshiftInferenceServiceReconciler(
		mgr.GetClient(),
		mgr.GetAPIReader(),
		ctrl.Log.WithName("controllers").WithName("InferenceService"),
		getEnvAsBool("MESH_DISABLED", false))).
		SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "InferenceService")
		os.Exit(1)
	}
	if err = (&controllers.StorageSecretReconciler{
		Client: mgr.GetClient(),
		Log:    ctrl.Log.WithName("controllers").WithName("StorageSecret"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "StorageSecret")
		os.Exit(1)
	}

	if err = (&controllers.KServeCustomCACertReconciler{
		Client: mgr.GetClient(),
		Log:    ctrl.Log.WithName("controllers").WithName("KServeCustomeCABundleConfigMap"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "KServeCustomeCABundleConfigMap")
		os.Exit(1)
	}

	if monitoringNS != "" {
		setupLog.Info("Monitoring namespace provided, setting up monitoring controller.")
		if err = (&controllers.MonitoringReconciler{
			Client:       mgr.GetClient(),
			Log:          ctrl.Log.WithName("controllers").WithName("MonitoringReconciler"),
			MonitoringNS: monitoringNS,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "MonitoringReconciler")
			os.Exit(1)
		}
	}

	if enableMRInferenceServiceReconcile {
		setupLog.Info("Model registry inference service reconciliation enabled..")
		if err = (controllers.NewModelRegistryInferenceServiceReconciler(
			mgr.GetClient(),
			ctrl.Log.WithName("controllers").WithName("ModelRegistryInferenceService"),
		)).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "ModelRegistryInferenceServiceReconciler")
			os.Exit(1)
		}
	} else {
		setupLog.Info("Model registry inference service reconciliation disabled. To enable model registry " +
			"reconciliation for InferenceService, please provide --model-registry-inference-reconcile flag.")
	}

	kserveWithMeshEnabled, kserveWithMeshEnabledErr := utils.VerifyIfComponentIsEnabled(context.Background(), mgr.GetClient(), utils.KServeWithServiceMeshComponent)
	if kserveWithMeshEnabledErr != nil {
		setupLog.Error(kserveWithMeshEnabledErr, "could not determine if kserve have service mesh enabled")
	}

	if kserveWithMeshEnabled {
		ksvcValidatorWebhookSetupErr := builder.WebhookManagedBy(mgr).
			For(&knservingv1.Service{}).
			WithValidator(webhook.NewKsvcValidator(mgr.GetClient())).
			Complete()
		if ksvcValidatorWebhookSetupErr != nil {
			setupLog.Error(err, "unable to setup Knative Service validating Webhook")
			os.Exit(1)
		}

	} else {
		setupLog.Info("Skipping setup of Knative Service validating/mutating Webhook, because KServe Serverless setup seems to be disabled in the DataScienceCluster resource.")
	}

	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}

	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
