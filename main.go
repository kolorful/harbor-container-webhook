/*
Copyright 2020 Indeed.
*/

package main

import (
	"crypto/tls"
	"flag"
	"os"
	"time"

	"github.com/hashicorp/go-cleanhttp"

	"indeed.com/devops-incubation/harbor-container-webhook/internal/mutate"

	admissionv1 "k8s.io/api/admission/v1"
	admissionv1beta1 "k8s.io/api/admission/v1beta1"

	"k8s.io/apimachinery/pkg/runtime"

	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	// +kubebuilder:scaffold:imports
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
	_ = admissionv1.AddToScheme(scheme)
	_ = admissionv1beta1.AddToScheme(scheme)
	// +kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr, harborAddr, certDir, resyncDur, timeoutDur string
	var enableLeaderElection, skipVerify bool
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&harborAddr, "harbor-addr", ":8080", "The address the harbor binds to.")
	flag.StringVar(&certDir, "cert-dir", "", "the directory that contains the server key and certificate.")
	flag.StringVar(&resyncDur, "resync-interval", "1m", "how often projects & proxy cache registry info is refreshed from the harbor API")
	flag.StringVar(&timeoutDur, "timeout", "1m", "default timeout for communicating with the harbor API")
	flag.BoolVar(&skipVerify, "skip-verify", false, "skip TLS certificate verification of harbor")
	flag.Parse()

	harborUser := os.Getenv("HARBOR_USER")
	harborPass := os.Getenv("HARBOR_PASS")
	resyncDuration := mustParseDuration(resyncDur)
	timeoutDuration := mustParseDuration(timeoutDur)

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:             scheme,
		MetricsBindAddress: metricsAddr,
		Port:               9443,
		LeaderElection:     enableLeaderElection,
		LeaderElectionID:   "harbor-container-webhook",
		CertDir:            certDir,
	})
	if err != nil {
		setupLog.Error(err, "unable to start harbor-container-webhook")
		os.Exit(1)
	}

	// +kubebuilder:scaffold:builder
	client := cleanhttp.DefaultClient()
	if skipVerify {
		transport := cleanhttp.DefaultTransport()
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
		client.Transport = transport
	}
	client.Timeout = timeoutDuration

	projectsCache := mutate.NewProjectsCache(client, harborAddr, harborUser, harborPass, resyncDuration)
	go func() {
		// query the cache once at startup to ensure it is filled
		_, _ = projectsCache.List()
	}()

	decoder, _ := admission.NewDecoder(scheme)
	mutate := mutate.PodContainerProxier{
		Client:         mgr.GetClient(),
		Cache:          projectsCache,
		Decoder:        decoder,
		HarborEndpoint: harborAddr,
	}

	mgr.GetWebhookServer().Register("/mutate-v1-pod", &webhook.Admission{Handler: &mutate})

	setupLog.Info("starting harbor-container-webhook")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running harbor-container-webhook")
		os.Exit(1)
	}
}

func mustParseDuration(s string) time.Duration {
	dur, err := time.ParseDuration(s)
	if err != nil {
		setupLog.Error(err, "invalid duration: "+s)
		os.Exit(1)
	}
	return dur
}
