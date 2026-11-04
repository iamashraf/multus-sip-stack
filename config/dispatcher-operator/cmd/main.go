// Command dispatcher-operator reconciles Kamailio's dispatcher list from
// FreeSWITCH's EndpointSlice.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"

	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/iamashraf/multus-sip-stack/config/dispatcher-operator/internal/controller"
	"github.com/iamashraf/multus-sip-stack/config/dispatcher-operator/internal/kamailio"
)

func main() {
	namespace := flag.String("namespace", "voice", "Namespace FreeSWITCH's EndpointSlice lives in.")
	freeswitchService := flag.String("freeswitch-service", "freeswitch", "Name of FreeSWITCH's Service.")
	kamailioTargets := flag.String("kamailio-targets", "", "Comma-separated name=url Kamailio dispatcher targets to sync, e.g. kamailio-a=http://kamailio-a:8080/RPC,kamailio-b=http://kamailio-b:8080/RPC.")
	dispatcherGroup := flag.Int("dispatcher-group", 1, "Kamailio dispatcher group ID to sync.")
	port := flag.Int("port", 5060, "SIP port to use in dispatcher URIs.")
	metricsAddr := flag.String("metrics-bind-address", ":8443", "Address the metrics endpoint binds to.")
	healthAddr := flag.String("health-probe-bind-address", ":8081", "Address the health probe endpoint binds to.")
	leaderElect := flag.Bool("leader-elect", true, "Enable leader election for the controller manager.")
	flag.Parse()

	ctrl.SetLogger(klog.NewKlogr())

	rawTargets, err := parseTargets(*kamailioTargets)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kamailio-targets: %v\n", err)
		os.Exit(1)
	}

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		fmt.Fprintf(os.Stderr, "add client-go scheme: %v\n", err)
		os.Exit(1)
	}
	if err := discoveryv1.AddToScheme(scheme); err != nil {
		fmt.Fprintf(os.Stderr, "add discoveryv1 scheme: %v\n", err)
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), buildManagerOptions(scheme, *namespace, *metricsAddr, *healthAddr, *leaderElect))
	if err != nil {
		fmt.Fprintf(os.Stderr, "new manager: %v\n", err)
		os.Exit(1)
	}

	targets := make(map[string]controller.TargetClient, len(rawTargets))
	for name, url := range rawTargets {
		targets[name] = kamailio.NewClient(url, http.DefaultClient)
	}

	reconciler := &controller.Reconciler{
		Client:            mgr.GetClient(),
		Namespace:         *namespace,
		FreeSwitchService: *freeswitchService,
		DispatcherGroup:   *dispatcherGroup,
		Port:              *port,
		Targets:           targets,
	}
	if err := reconciler.SetupWithManager(mgr); err != nil {
		fmt.Fprintf(os.Stderr, "setup reconciler: %v\n", err)
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		fmt.Fprintf(os.Stderr, "add healthz check: %v\n", err)
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		fmt.Fprintf(os.Stderr, "add readyz check: %v\n", err)
		os.Exit(1)
	}

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		fmt.Fprintf(os.Stderr, "manager exited: %v\n", err)
		os.Exit(1)
	}
}

// buildManagerOptions builds the controller-runtime manager options. The
// cache is restricted to namespace: the EndpointSlice informer must only
// list/watch there, matching the namespaced RBAC Role (no ClusterRole is
// granted for endpointslices).
func buildManagerOptions(scheme *runtime.Scheme, namespace, metricsAddr, healthAddr string, leaderElect bool) ctrl.Options {
	return ctrl.Options{
		Scheme:                  scheme,
		LeaderElection:          leaderElect,
		LeaderElectionID:        "dispatcher-operator-lock",
		LeaderElectionNamespace: namespace,
		Metrics:                 metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress:  healthAddr,
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{namespace: {}},
		},
	}
}

// parseTargets parses a --kamailio-targets flag value of the form
// "name=url,name=url" into a map of target name to Kamailio JSON-RPC URL.
func parseTargets(raw string) (map[string]string, error) {
	if raw == "" {
		return nil, fmt.Errorf("must not be empty")
	}
	targets := make(map[string]string)
	for _, pair := range strings.Split(raw, ",") {
		name, url, ok := strings.Cut(pair, "=")
		if !ok || name == "" || url == "" {
			return nil, fmt.Errorf("invalid entry %q, want name=url", pair)
		}
		if _, exists := targets[name]; exists {
			return nil, fmt.Errorf("duplicate target name %q", name)
		}
		targets[name] = url
	}
	return targets, nil
}
