package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"time"

	datav1alpha1 "github.com/devantler-tech/data-product-controller/api/v1alpha1"
	"github.com/devantler-tech/data-product-controller/internal/catalog"
	"github.com/devantler-tech/data-product-controller/internal/config"
	productcontroller "github.com/devantler-tech/data-product-controller/internal/controller"
	providerv1 "github.com/devantler-tech/data-product-controller/internal/provider/v1"
	"github.com/devantler-tech/data-product-controller/internal/registry"
	"github.com/devantler-tech/data-product-controller/pkg/featureflag"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

const provisionedSourcesFlag = "provisioned-sources"

const engineProvidersFlag = "engine-providers"

const connectorReadinessFlag = "connector-readiness"

const contractReadinessFlag = "contract-readiness"

const compositionFlag = "composition"

const (
	dcatCatalogFlag       = "dcat-catalog"
	uiContractFlag        = "ui-contract"
	uiAppearanceFlag      = "ui-appearance"
	registryDiscoveryFlag = "registry-discovery"
	registryLineageFlag   = "registry-lineage"
)

// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,namespace=data-product-system,verbs=get;list;watch;create;update;patch;delete

// main validates release flags, registers the controller and registry, and runs the manager until shutdown.
// main validates release gates before starting the controller manager and read-only registry.
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

	sourcesEnabled, err := config.ProvisionedSourcesEnabled(
		os.Getenv("PROVISIONED_SOURCES_ENABLED"),
	)
	if err != nil {
		setupLog.Error(err, "invalid provisioned sources configuration")
		os.Exit(1)
	}
	engineProvidersEnabled, err := config.EngineProvidersEnabled(
		os.Getenv("ENGINE_PROVIDERS_ENABLED"),
	)
	if err != nil {
		setupLog.Error(err, "invalid engine provider configuration")
		os.Exit(1)
	}
	connectorsEnabled, err := config.ConnectorReadinessEnabled(
		os.Getenv("CONNECTOR_READINESS_ENABLED"),
	)
	if err != nil {
		setupLog.Error(err, "invalid connector readiness configuration")
		os.Exit(1)
	}
	contractsEnabled, err := config.ContractReadinessEnabled(
		os.Getenv("CONTRACT_READINESS_ENABLED"),
	)
	if err != nil {
		setupLog.Error(err, "invalid contract readiness configuration")
		os.Exit(1)
	}
	compositionEnabled, err := config.CompositionEnabled(os.Getenv("COMPOSITION_ENABLED"))
	if err != nil {
		setupLog.Error(err, "invalid composition configuration")
		os.Exit(1)
	}
	dcatCatalogEnabled, err := config.DCATCatalogEnabled(os.Getenv("DCAT_CATALOG_ENABLED"))
	if err != nil {
		setupLog.Error(err, "invalid DCAT catalog configuration")
		os.Exit(1)
	}
	uiContractEnabled, err := config.UIContractEnabled(os.Getenv("UI_CONTRACT_ENABLED"))
	if err != nil {
		setupLog.Error(err, "invalid UI contract configuration")
		os.Exit(1)
	}
	uiAppearanceEnabled, err := config.UIAppearanceEnabled(os.Getenv("UI_APPEARANCE_ENABLED"))
	if err != nil {
		setupLog.Error(err, "invalid UI appearance configuration")
		os.Exit(1)
	}
	registryDiscoveryEnabled, err := config.RegistryDiscoveryEnabled(
		os.Getenv("REGISTRY_DISCOVERY_ENABLED"),
	)
	if err != nil {
		setupLog.Error(err, "invalid registry discovery configuration")
		os.Exit(1)
	}
	registryLineageEnabled, err := config.RegistryLineageEnabled(
		os.Getenv("REGISTRY_LINEAGE_ENABLED"),
	)
	if err != nil {
		setupLog.Error(err, "invalid registry lineage configuration")
		os.Exit(1)
	}
	flagProvider := featureflag.NewProvider(
		map[string]bool{
			provisionedSourcesFlag: sourcesEnabled,
			engineProvidersFlag:    engineProvidersEnabled,
			connectorReadinessFlag: connectorsEnabled,
			contractReadinessFlag:  contractsEnabled,
			compositionFlag:        compositionEnabled,
			dcatCatalogFlag:        dcatCatalogEnabled,
			uiContractFlag:         uiContractEnabled,
			uiAppearanceFlag:       uiAppearanceEnabled,
			registryDiscoveryFlag:  registryDiscoveryEnabled,
			registryLineageFlag:    registryLineageEnabled,
		},
	)
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

	managerConfig := ctrl.GetConfigOrDie()
	engineReader, err := providerv1.NewEngineReader(managerConfig)
	if err != nil {
		setupLog.Error(err, "create engine provider reader")
		os.Exit(1)
	}
	controllerManager, err := ctrl.NewManager(
		providerv1.MetadataOnlyConfig(managerConfig),
		ctrl.Options{
			Scheme:                 scheme,
			Metrics:                metricsserver.Options{BindAddress: metricsAddress},
			HealthProbeBindAddress: probeAddress,
			LeaderElection:         leaderElection,
			LeaderElectionID:       "data-product-controller.data.devantler.tech",
		},
	)
	if err != nil {
		setupLog.Error(err, "create controller manager")
		os.Exit(1)
	}

	reconciler := &productcontroller.DataProductReconciler{
		Client:       controllerManager.GetClient(),
		Scheme:       controllerManager.GetScheme(),
		SourceReader: controllerManager.GetAPIReader(),
		SourceProvider: &providerv1.Registry{
			Reader:       engineReader,
			LegacyReader: controllerManager.GetAPIReader(),
			Mapper:       controllerManager.GetRESTMapper(),
		},
		EngineProvidersEnabled: func(ctx context.Context) bool {
			return featureflag.Enabled(ctx, flagClient, engineProvidersFlag)
		},
		ConnectorReader: controllerManager.GetAPIReader(),
		CompositionEnabled: func(ctx context.Context) bool {
			return featureflag.Enabled(ctx, flagClient, compositionFlag)
		},
		ContractsEnabled: func(ctx context.Context) bool {
			return featureflag.Enabled(ctx, flagClient, contractReadinessFlag)
		},
		ConnectorsEnabled: func(ctx context.Context) bool {
			return featureflag.Enabled(ctx, flagClient, connectorReadinessFlag)
		},
		SourcesEnabled: func(ctx context.Context) bool {
			return featureflag.Enabled(ctx, flagClient, provisionedSourcesFlag)
		},
	}
	if err := reconciler.SetupWithManager(controllerManager); err != nil {
		setupLog.Error(err, "register data-product controller")
		os.Exit(1)
	}

	registryHandler := registry.NewHandlerWithOptions(
		controllerManager.GetAPIReader(),
		registry.HandlerOptions{
			InputCompatibility: productcontroller.DeclaredInputCompatibility,
			LineageEnabled: func(ctx context.Context) bool {
				return featureflag.Enabled(ctx, flagClient, registryLineageFlag)
			},
			DiscoveryEnabled: func(ctx context.Context) bool {
				return featureflag.Enabled(ctx, flagClient, registryDiscoveryFlag)
			},
			ContractEnabled: func(ctx context.Context) bool {
				return featureflag.Enabled(ctx, flagClient, uiContractFlag)
			},
			AppearanceEnabled: func(ctx context.Context) bool {
				return featureflag.Enabled(ctx, flagClient, uiAppearanceFlag)
			},
		},
	)
	catalogHandler, err := catalog.NewHandler(controllerManager.GetAPIReader(), catalog.Options{
		ID: os.Getenv("DCAT_CATALOG_ID"),
		Enabled: func(ctx context.Context) bool {
			return featureflag.Enabled(ctx, flagClient, dcatCatalogFlag)
		},
	})
	if err != nil {
		setupLog.Error(err, "configure DCAT catalog")
		os.Exit(1)
	}
	registryMux := http.NewServeMux()
	registryMux.Handle("GET /api/v1/catalog", catalogHandler)
	registryMux.Handle("/", registryHandler)
	registryServer := &http.Server{
		Addr:              registryAddress,
		Handler:           registryMux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if err := controllerManager.Add(newRegistryServer(registryServer)); err != nil {
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
