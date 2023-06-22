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
	inferenceservicev1 "github.com/kserve/modelmesh-serving/apis/serving/v1beta1"
	config2 "github.com/kserve/modelmesh-serving/pkg/config"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"k8s.io/apimachinery/pkg/types"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"strconv"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	predictorv1 "github.com/kserve/modelmesh-serving/apis/serving/v1alpha1"
	"github.com/opendatahub-io/odh-model-controller/controllers"
	routev1 "github.com/openshift/api/route/v1"
	corev1 "k8s.io/api/core/v1"
	authv1 "k8s.io/api/rbac/v1"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

const (
	ControllerNamespaceEnvVar  = "NAMESPACE"
	DefaultControllerNamespace = "model-serving"
	KubeNamespaceFile          = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
	UserConfigMapName          = "model-serving-config"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(inferenceservicev1.AddToScheme(scheme))
	utilruntime.Must(predictorv1.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(routev1.AddToScheme(scheme))
	utilruntime.Must(authv1.AddToScheme(scheme))
	utilruntime.Must(monitoringv1.AddToScheme(scheme))

	// The following are related to Service Mesh, uncomment this and other
	// similar blocks to use with Service Mesh
	//utilruntime.Must(virtualservicev1.AddToScheme(scheme))
	//utilruntime.Must(maistrav1.AddToScheme(scheme))

	//+kubebuilder:scaffold:scheme
}

func getEnvAsBool(name string, defaultValue bool) bool {
	valStr := os.Getenv(name)
	if val, err := strconv.ParseBool(valStr); err == nil {
		return val
	}
	return defaultValue
}

func main() {

	// ----- fetch controller namespace -----
	controllerNamespace := os.Getenv(ControllerNamespaceEnvVar)
	controllerNamespace = "opendatahub"
	if controllerNamespace == "" {
		bytes, err := os.ReadFile(KubeNamespaceFile)
		if err != nil {
			//TODO check kube context and retrieve namespace from there
			setupLog.Info("Error reading Kube-mounted namespace file, reverting to default namespace",
				"file", KubeNamespaceFile, "err", err, "default", DefaultControllerNamespace)
			controllerNamespace = DefaultControllerNamespace
		} else {
			controllerNamespace = string(bytes)
		}
	}

	var metricsAddr string
	var enableLeaderElection bool
	var monitoringNS string
	var probeAddr string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&monitoringNS, "monitoring-namespace", "",
		"The Namespace where the monitoring stack's Prometheus resides.")
	flag.StringVar(&monitoringNS, "apps-namespace", "",
		"The Namespace where odh apps reside.")

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

	cfg := config.GetConfigOrDie()
	cl, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		setupLog.Error(err, "unable to create an api server client")
		os.Exit(1)
	}

	customConfigMapName := types.NamespacedName{Name: UserConfigMapName, Namespace: controllerNamespace}
	cp, err := config2.NewConfigProvider(context.Background(), cl, customConfigMapName)
	if err != nil {
		setupLog.Error(err, "Error loading user config from configmap", "ConfigMapName", UserConfigMapName)
		os.Exit(1)
	}

	//Setup InferenceService controller
	if err = (&controllers.OpenshiftInferenceServiceReconciler{
		Client:              mgr.GetClient(),
		Log:                 ctrl.Log.WithName("controllers").WithName("InferenceService"),
		Scheme:              mgr.GetScheme(),
		ConfigProvider:      cp,
		ControllerNamespace: controllerNamespace,
		CustomConfigMapName: customConfigMapName,
		MeshDisabled:        getEnvAsBool("MESH_DISABLED", false),
	}).SetupWithManager(mgr); err != nil {
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
	} else {
		setupLog.Info("Monitoring namespace not provided, skipping setup of monitoring controller. To enable " +
			"monitoring for ModelServing, please provide a monitoring namespace via the (--monitoring-namespace) flag.")
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
