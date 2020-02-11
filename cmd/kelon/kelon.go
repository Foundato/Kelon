package main

import (
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Foundato/kelon/pkg/monitoring"

	apiInt "github.com/Foundato/kelon/internal/pkg/api"
	opaInt "github.com/Foundato/kelon/internal/pkg/opa"
	requestInt "github.com/Foundato/kelon/internal/pkg/request"
	translateInt "github.com/Foundato/kelon/internal/pkg/translate"
	watcherInt "github.com/Foundato/kelon/internal/pkg/watcher"

	"github.com/Foundato/kelon/common"
	"github.com/Foundato/kelon/configs"
	"github.com/Foundato/kelon/internal/pkg/api/envoy"
	"github.com/Foundato/kelon/internal/pkg/api/istio"
	"github.com/Foundato/kelon/internal/pkg/data"
	"github.com/Foundato/kelon/internal/pkg/util"
	"github.com/Foundato/kelon/pkg/api"
	"github.com/Foundato/kelon/pkg/opa"
	"github.com/Foundato/kelon/pkg/request"
	"github.com/Foundato/kelon/pkg/translate"
	"github.com/Foundato/kelon/pkg/watcher"
	log "github.com/sirupsen/logrus"
	"gopkg.in/alecthomas/kingpin.v2"
)

//nolint:gochecknoglobals
var (
	app             = kingpin.New("kelon", "Kelon policy enforcer.")
	opaPath         = app.Flag("opa-conf", "Path to the OPA configuration yaml.").Short('o').Default("./opa.yml").Envar("OPA_CONF").ExistingFile()
	regoDir         = app.Flag("rego-dir", "Dir containing .rego files which will be loaded into OPA.").Short('r').Envar("REGO_DIR").ExistingDir()
	pathPrefix      = app.Flag("path-prefix", "Prefix which is used to proxy OPA's Data-API.").Default("/v1").Envar("PATH_PREFIX").String()
	port            = app.Flag("port", "Port on which the proxy endpoint is served.").Short('p').Default("8181").Envar("PORT").Uint32()
	preprocessRegos = app.Flag("preprocess-policies", "Preprocess incoming policies for internal use-case (EXPERIMENTAL FEATURE! DO NOT USE!).").Default("false").Envar("PREPROCESS_POLICIES").Bool()
	logLevel        = app.Flag("log-level", "Log-Level for Kelon. Must be one of [DEBUG, INFO, WARN, ERROR]").Default("INFO").Envar("LOG_LEVEL").Enum("DEBUG", "INFO", "WARN", "ERROR", "debug", "info", "warn", "error")

	// Configs for envoy external auth
	envoyPort       = app.Flag("envoy-port", "Also start Envoy GRPC-Proxy on specified port so integrate kelon with Istio.").Envar("ENVOY_PORT").Uint32()
	envoyDryRun     = app.Flag("envoy-dry-run", "Enable/Disable the dry run feature of the envoy-proxy.").Default("false").Envar("ENVOY_DRY_RUN").Bool()
	envoyReflection = app.Flag("envoy-reflection", "Enable/Disable the reflection feature of the envoy-proxy.").Default("true").Envar("ENVOY_REFLECTION").Bool()

	// Configs for Istio Mixer Adapter
	istioPort            = app.Flag("istio-port", "Also start Istio Mixer Out of Tree Adapter  on specified port so integrate kelon with Istio.").Envar("ISTIO_PORT").Uint32()
	istioCredentialFile  = app.Flag("istio-credential-file", "Filepath containing istio credentials for mTLS (i.e. adapter.crt).").Envar("ISTIO_CREDENTIAL_FILE").ExistingFile()
	istioPrivateKeyFile  = app.Flag("istio-private-key-file", "Filepath containing istio private key for mTLS (i.e. adapter.key).").Envar("ISTIO_PRIVATE_KEY_FILE").ExistingFile()
	istioCertificateFile = app.Flag("istio-certificate-file", "Filepath containing istio certificate for mTLS (i.e. ca.pem).").Envar("ISTIO_CERTIFICATE_FILE").ExistingFile()

	// Configs for monitoring
	metricsService              = app.Flag("metrics-service", "Service that is used for monitoring [Prometheus, ApplicationInsights]").Envar("METRICS_SERVICE").Enum("Prometheus", "prometheus", "ApplicationInsights", "applicationinsights")
	instrumentationKey          = app.Flag("instrumentation-key", "The ApplicationInsights-InstrumentationKey that is used to connect to the API.").Envar("INSTRUMENTATION_KEY").String()
	appInsightsMaxBatchSize     = app.Flag("application-insights-max-batch-size", "Configure how many items can be sent in one call to the data collector.").Default("8192").Envar("APPLICATION_INSIGHTS_MAX_BATCH_SIZE").Int()
	appInsightsMaxBatchInterval = app.Flag("application-insights-max-batch-interval-seconds", "Configure the maximum delay before sending queued telemetry.").Default("2").Envar("APPLICATION_INSIGHTS_MAX_BATCH_INTERVAL_SECONDS").Int()

	proxy         api.ClientProxy       = nil
	envoyProxy    api.ClientProxy       = nil
	istioProxy    api.ClientProxy       = nil
	configWatcher watcher.ConfigWatcher = nil
)

func main() {
	// Configure kingpin
	var (
		// Commands
		run = app.Command("run", "Run kelon in production mode.")
		// Flags
		datastorePath     = app.Flag("datastore-conf", "Path to the datastore configuration yaml.").Short('d').Default("./datastore.yml").Envar("DATASTORE_CONF").ExistingFile()
		apiPath           = app.Flag("api-conf", "Path to the api configuration yaml.").Short('a').Default("./api.yml").Envar("API_CONF").ExistingFile()
		configWatcherPath = app.Flag("config-watcher-path", "Path where the config watcher should listen for changes.").Envar("CONFIG_WATCHER_PATH").ExistingDir()
	)

	app.HelpFlag.Short('h')
	app.Version(common.Version)

	// Process args and initialize logger
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp: true,
	})

	switch kingpin.MustParse(app.Parse(os.Args[1:])) {
	case run.FullCommand():
		log.SetOutput(os.Stdout)
		log.Infof("Kelon starting with log level %q...", *logLevel)
		switch strings.ToUpper(*logLevel) {
		case "INFO":
			log.SetLevel(log.InfoLevel)
		case "DEBUG":
			log.SetLevel(log.DebugLevel)
		case "WARN":
			log.SetLevel(log.WarnLevel)
		case "ERROR":
			log.SetLevel(log.ErrorLevel)
		}

		// Init config loader
		configLoader := configs.FileConfigLoader{
			DatastoreConfigPath: *datastorePath,
			APIConfigPath:       *apiPath,
		}
		// Start app after config is present
		makeConfigWatcher(configLoader, configWatcherPath)
		configWatcher.Watch(onConfigLoaded)
		stopOnSIGTERM()
	}
}

func onConfigLoaded(change watcher.ChangeType, loadedConf *configs.ExternalConfig, err error) {
	if err != nil {
		log.Fatalln("Unable to parse configuration: ", err.Error())
	}

	if change == watcher.ChangeAll {
		// Configure application
		var (
			config          = new(configs.AppConfig)
			compiler        = opaInt.NewPolicyCompiler()
			parser          = requestInt.NewURLProcessor()
			mapper          = requestInt.NewPathMapper()
			translator      = translateInt.NewAstTranslator()
			metricsProvider monitoring.MetricsProvider
		)

		if metricsService != nil {
			switch strings.ToLower(*metricsService) {
			case "prometheus":
				metricsProvider = &monitoring.Prometheus{}
			case "applicationinsights":
				if instrumentationKey == nil {
					log.Fatalln("Kelon was started with ApplicationInsights as --metrics-service but no option --application-insights-key was provided!")
				}
				metricsProvider = &monitoring.ApplicationInsights{
					AppInsightsInstrumentationKey: *instrumentationKey,
					MaxBatchSize:                  *appInsightsMaxBatchSize,
					MaxBatchIntervalSeconds:       *appInsightsMaxBatchInterval,
				}
			}
		}

		// Build configs
		config.API = loadedConf.API
		config.Data = loadedConf.Data
		config.MetricsProvider = metricsProvider
		serverConf := makeServerConfig(compiler, parser, mapper, translator, loadedConf)

		if *preprocessRegos {
			*regoDir = util.PrepocessPoliciesInDir(config, *regoDir)
		}

		// Start rest proxy
		startNewRestProxy(config, &serverConf)

		// Start envoyProxy proxy in addition to rest proxy as soon as a port was specified!
		if envoyPort != nil && *envoyPort != 0 {
			startNewEnvoyProxy(config, &serverConf)
		}

		// Start istio adapter in addition to rest proxy as soon as a port was specified!
		if istioPort != nil && *istioPort != 0 {
			startNewIstioAdapter(config, &serverConf)
		}
	}
}

func makeConfigWatcher(configLoader configs.FileConfigLoader, configWatcherPath *string) {
	if regoDir == nil || *regoDir == "" {
		configWatcher = watcherInt.NewSimple(configLoader)
	} else {
		// Set configWatcherPath to rego path by default
		if configWatcherPath == nil || *configWatcherPath == "" {
			configWatcherPath = regoDir
		}
		configWatcher = watcherInt.NewFileWatcher(configLoader, *configWatcherPath)
	}
}

func startNewRestProxy(appConfig *configs.AppConfig, serverConf *api.ClientProxyConfig) {
	// Create Rest proxy and start
	proxy = apiInt.NewRestProxy(*pathPrefix, int32(*port))
	if err := proxy.Configure(appConfig, serverConf); err != nil {
		log.Fatalln(err.Error())
	}
	// Start proxy
	if err := proxy.Start(); err != nil {
		log.Fatalln(err.Error())
	}
}

func startNewEnvoyProxy(appConfig *configs.AppConfig, serverConf *api.ClientProxyConfig) {
	if *envoyPort == *port {
		panic("Cannot start envoyProxy proxy and rest proxy on same port!")
	}
	if *envoyPort == *istioPort {
		panic("Cannot start envoyProxy proxy and istio adapter on same port!")
	}

	// Create Rest proxy and start
	envoyProxy = envoy.NewEnvoyProxy(envoy.EnvoyConfig{
		Port:             *envoyPort,
		DryRun:           *envoyDryRun,
		EnableReflection: *envoyReflection,
	})
	if err := envoyProxy.Configure(appConfig, serverConf); err != nil {
		log.Fatalln(err.Error())
	}
	// Start proxy
	if err := envoyProxy.Start(); err != nil {
		log.Fatalln(err.Error())
	}
}

func startNewIstioAdapter(appConfig *configs.AppConfig, serverConf *api.ClientProxyConfig) {
	if *istioPort == *port {
		panic("Cannot start istio adapter and rest proxy on same port!")
	}
	if *envoyPort == *istioPort {
		panic("Cannot start envoyProxy proxy and istio adapter on same port!")
	}

	var tlsConfig *istio.MutualTLSConfig = nil
	if *istioCertificateFile != "" || *istioPrivateKeyFile != "" || *istioCredentialFile != "" {
		if *istioCertificateFile == "" {
			log.Fatalf("Isito mutual TLS configured, but no istioCertificateFile specified!")
		}
		if *istioPrivateKeyFile == "" {
			log.Fatalf("Isito mutual TLS configured, but no istioPrivateKeyFile specified!")
		}
		if *istioCredentialFile == "" {
			log.Fatalf("Isito mutual TLS configured, but no istioCredentialFile specified!")
		}

		tlsConfig = &istio.MutualTLSConfig{
			CredentialFile:  *istioCredentialFile,
			PrivateKeyFile:  *istioPrivateKeyFile,
			CertificateFile: *istioCertificateFile,
		}
	}

	// Create Rest proxy and start
	if createdProxy, err := istio.NewKelonIstioAdapter(*istioPort, tlsConfig); err != nil {
		log.Fatalln(err.Error())
	} else {
		istioProxy = createdProxy
	}
	if err := istioProxy.Configure(appConfig, serverConf); err != nil {
		log.Fatalln(err.Error())
	}
	// Start proxy
	if err := istioProxy.Start(); err != nil {
		log.Fatalln(err.Error())
	}
}

func makeServerConfig(compiler opa.PolicyCompiler, parser request.PathProcessor, mapper request.PathMapper, translator translate.AstTranslator, loadedConf *configs.ExternalConfig) api.ClientProxyConfig {
	// Build server config
	serverConf := api.ClientProxyConfig{
		Compiler: &compiler,
		PolicyCompilerConfig: opa.PolicyCompilerConfig{
			Prefix:        pathPrefix,
			OpaConfigPath: opaPath,
			RegoDir:       regoDir,
			ConfigWatcher: &configWatcher,
			PathProcessor: &parser,
			PathProcessorConfig: request.PathProcessorConfig{
				PathMapper: &mapper,
			},
			Translator: &translator,
			AstTranslatorConfig: translate.AstTranslatorConfig{
				Datastores: data.MakeDatastores(loadedConf.Data),
			},
		},
	}
	return serverConf
}

func stopOnSIGTERM() {
	interruptChan := make(chan os.Signal, 1)
	signal.Notify(interruptChan, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	// Block until we receive our signal.
	<-interruptChan

	log.Infoln("Caught SIGTERM...")
	// Stop envoyProxy proxy if started
	if envoyProxy != nil {
		if err := envoyProxy.Stop(time.Second * 10); err != nil {
			log.Warnln(err.Error())
		}
	}

	// Stop rest proxy if started
	if proxy != nil {
		if err := proxy.Stop(time.Second * 10); err != nil {
			log.Warnln(err.Error())
		}
	}

	// Give components enough time for graceful shutdown
	// This terminates earlier, because rest-proxy prints FATAL if http-server is closed
	time.Sleep(5 * time.Second)
}
