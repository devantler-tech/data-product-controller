package main

import (
	"context"
	"errors"
	"flag"
	"net/http"
	"os"
	"time"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"github.com/devantler-tech/data-product-controller/internal/config"
	productcontroller "github.com/devantler-tech/data-product-controller/internal/controller"
	"github.com/devantler-tech/data-product-controller/internal/registry"
	"github.com/devantler-tech/data-product-controller/pkg/featureflag"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

const registryUIFlag = "registry-ui"

// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,namespace=data-product-system,verbs=get;list;watch;create;update;patch;delete

func main() {
	var metricsAddress string
	var probeAddress string
	var registryAddress string
	var leaderElection bool

	flag.StringVar(
		&metricsAddress,
		"metrics-bind-address",
		":8080",
		"Address for Prometheus metrics.",
	)
	flag.StringVar(
		&probeAddress,
		"health-probe-bind-address",
		":8081",
		"Address for health probes.",
	)
	flag.StringVar(
		&registryAddress,
		"registry-bind-address",
		":8082",
		"Address for the descriptor registry and UI.",
	)
	flag.BoolVar(
		&leaderElection,
		"leader-elect",
		false,
		"Use leader election for the controller manager.",
	)
	zapOptions := zap.Options{Development: false}
	zapOptions.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOptions)))
	setupLog := ctrl.Log.WithName("setup")

	uiEnabled, err := config.RegistryUIEnabled(os.Getenv("REGISTRY_UI_ENABLED"))
	if err != nil {
		setupLog.Error(err, "invalid registry UI configuration")
		os.Exit(1)
	}

	flagProvider := featureflag.NewProvider(map[string]bool{registryUIFlag: uiEnabled})
	flagClient, err := featureflag.NewClient("data-product-controller", flagProvider)
	if err != nil {
		setupLog.Error(err, "configure feature flags")
		os.Exit(1)
	}

	scheme := clientgoscheme.Scheme
	if err := datav1alpha1.AddToScheme(scheme); err != nil {
		setupLog.Error(err, "register data-product API")
		os.Exit(1)
	}

	controllerManager, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddress},
		HealthProbeBindAddress: probeAddress,
		LeaderElection:         leaderElection,
		LeaderElectionID:       "data-product-controller.data.devantler.tech",
	})
	if err != nil {
		setupLog.Error(err, "create controller manager")
		os.Exit(1)
	}

	reconciler := &productcontroller.DataProductReconciler{
		Client: controllerManager.GetClient(),
		Scheme: controllerManager.GetScheme(),
	}
	if err := reconciler.SetupWithManager(controllerManager); err != nil {
		setupLog.Error(err, "register data-product controller")
		os.Exit(1)
	}

	registryHandler := registry.NewHandler(
		controllerManager.GetAPIReader(),
		func(ctx context.Context) bool {
			return featureflag.Enabled(ctx, flagClient, registryUIFlag)
		},
	)
	registryServer := &http.Server{
		Addr:              registryAddress,
		Handler:           registryHandler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err := controllerManager.Add(manager.RunnableFunc(func(ctx context.Context) error {
		go func() {
			<-ctx.Done()

			shutdownContext, cancel := context.WithTimeout(
				context.WithoutCancel(ctx),
				10*time.Second,
			)
			defer cancel()

			if err := registryServer.Shutdown(shutdownContext); err != nil {
				setupLog.Error(err, "shut down registry server")
			}
		}()

		setupLog.Info(
			"starting descriptor registry",
			"address",
			registryAddress,
			"uiEnabled",
			uiEnabled,
		)
		if err := registryServer.ListenAndServe(); err != nil &&
			!errors.Is(err, http.ErrServerClosed) {
			return err
		}

		return nil
	})); err != nil {
		setupLog.Error(err, "register descriptor registry")
		os.Exit(1)
	}

	if err := controllerManager.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "register health check")
		os.Exit(1)
	}
	if err := controllerManager.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "register readiness check")
		os.Exit(1)
	}

	setupLog.Info("starting data-product controller")
	if err := controllerManager.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "run controller manager")
		os.Exit(1)
	}
}
