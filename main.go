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
	"flag"
	"os"
	"strconv"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
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
// +kubebuilder:rbac:groups=security.istio.io,resources=peerauthentications,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=telemetry.istio.io,resources=telemetries,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=maistra.io,resources=servicemeshmembers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=maistra.io,resources=servicemeshmembers/finalizers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=maistra.io,resources=servicemeshmemberrolls,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=maistra.io,resources=servicemeshcontrolplanes,verbs=get;list;watch;create;update;patch;use
// +kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=route.openshift.io,resources=routes/custom-host,verbs=create
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterrolebindings;rolebindings,verbs=get;list;watch;create;update;patch;watch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors;podmonitors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=extensions,resources=ingresses,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces;pods;services;endpoints,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",resources=secrets;configmaps;serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=authorino.kuadrant.io,resources=authconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=datasciencecluster.opendatahub.io,resources=datascienceclusters,verbs=get;list;watch

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
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Port:                   9443,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "odh-model-controller",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	//Setup InferenceService controller
	if err = (controllers.NewOpenshiftInferenceServiceReconciler(
		mgr.GetClient(),
		mgr.GetScheme(),
		ctrl.Log.WithName("controllers").WithName("InferenceService"),
		getEnvAsBool("MESH_DISABLED", false))).
		SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "InferenceService")
		os.Exit(1)
	}
	if err = (&controllers.StorageSecretReconciler{
		Client: mgr.GetClient(),
		Log:    ctrl.Log.WithName("controllers").WithName("StorageSecret"),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "StorageSecret")
		os.Exit(1)
	}

	if err = (&controllers.KServeCustomCACertReconciler{
		Client: mgr.GetClient(),
		Log:    ctrl.Log.WithName("controllers").WithName("KServeCustomeCABundleConfigMap"),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "KServeCustomeCABundleConfigMap")
		os.Exit(1)
	}

	if monitoringNS != "" {
		setupLog.Info("Monitoring namespace provided, setting up monitoring controller.")
		if err = (&controllers.MonitoringReconciler{
			Client:       mgr.GetClient(),
			Log:          ctrl.Log.WithName("controllers").WithName("MonitoringReconciler"),
			Scheme:       mgr.GetScheme(),
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
			mgr.GetScheme(),
			ctrl.Log.WithName("controllers").WithName("ModelRegistryInferenceService"),
		)).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "ModelRegistryInferenceServiceReconciler")
			os.Exit(1)
		}
	} else {
		setupLog.Info("Model registry inference service reconciliation disabled. To enable model registry " +
			"reconciliation for InferenceService, please provide --model-registry-inference-reconcile flag.")
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
