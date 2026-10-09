package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"open-mihomo-gateway/internal/controlapi"
	"open-mihomo-gateway/internal/linuxnetwork"
	"open-mihomo-gateway/internal/qnaphost"
	"open-mihomo-gateway/internal/webgateway"
	"open-mihomo-gateway/internal/webui"
)

func main() {
	if _, err := webgateway.ParseNASPlatform(os.Getenv("OPENSURGE_NAS_PLATFORM")); err != nil {
		fatal(err)
	}
	component := flag.String("component", "all", "component to run: all, control, web, token, recover, health, or platform")
	configPath := flag.String("config", "/data/config/opensurge.yaml", "path to persistent gateway config")
	storeDir := flag.String("store", "/data/control", "persistent privileged control directory")
	authDir := flag.String("auth-dir", "", "persistent Web authentication directory")
	controlAddr := flag.String("control-addr", "127.0.0.1:61767", "loopback-only privileged Control API address")
	webAddr := flag.String("web-addr", "0.0.0.0:8080", "LAN-facing Web address")
	webAuth := flag.Bool("web-auth", false, "require administrator login instead of the default local password-free Web")
	allowedHosts := flag.String("allowed-hosts", "", "comma-separated additional hostnames accepted by the Web gateway")
	controlTokenFlag := flag.String("control-token", "", "internal token supplied to the unprivileged Web component")
	requireBootstrapToken := flag.Bool("require-bootstrap-token", false, "require a one-time token before first administrator setup")
	secureCookies := flag.Bool("secure-cookies", false, "mark administrator cookies Secure for HTTPS reverse-proxy deployments")
	qnapOnly := flag.Bool("qnap-only", false, "hide desktop-only Control API routes from the LAN Web surface")
	flag.Parse()

	if strings.TrimSpace(*authDir) == "" {
		*authDir = *storeDir
	}
	if *component == "token" {
		token, err := controlapi.NewStore(*storeDir).Token()
		if err != nil {
			fatal(fmt.Errorf("load internal control token: %w", err))
		}
		fmt.Print(token)
		return
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	switch *component {
	case "platform":
		platform, _ := webgateway.ParseNASPlatform(os.Getenv("OPENSURGE_NAS_PLATFORM"))
		fmt.Println(platform)
	case "recover":
		recovered, err := controlapi.RecoverContainerConfigAfterRestart(ctx, *configPath, *storeDir)
		if err != nil {
			fatal(fmt.Errorf("automatic gateway recovery failed: %w", err))
		}
		if recovered {
			fmt.Println("OpenSurge gateway automatically recovered after container restart.")
		} else {
			fmt.Println("OpenSurge gateway recovery: no data-plane restart required.")
		}
	case "health":
		if err := checkWebReadiness(ctx, *webAddr); err != nil {
			fatal(fmt.Errorf("gateway readiness check failed: %w", err))
		}
		fmt.Println("gateway ready")
	case "control":
		control, hostManager, err := newControl(*configPath, *storeDir, *controlAddr)
		if err != nil {
			fatal(err)
		}
		if hostManager != nil {
			go hostManager.Run(ctx)
		}
		fmt.Printf("OpenSurge privileged control: http://%s (loopback only)\n", *controlAddr)
		serveErr := control.Serve(ctx)
		cancel()
		releaseHostRouting(hostManager)
		if serveErr != nil {
			fatal(serveErr)
		}
	case "web":
		if strings.TrimSpace(*controlTokenFlag) == "" {
			fatal(fmt.Errorf("--control-token is required for the standalone Web component"))
		}
		gateway, err := newWeb(*webAddr, *controlAddr, *controlTokenFlag, *authDir, *allowedHosts, *webAuth, *requireBootstrapToken, *secureCookies, *qnapOnly)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("OpenSurge Web: http://%s\n", *webAddr)
		if err := gateway.Serve(ctx); err != nil {
			fatal(err)
		}
	case "all":
		control, hostManager, err := newControl(*configPath, *storeDir, *controlAddr)
		if err != nil {
			fatal(err)
		}
		controlToken := strings.TrimSpace(*controlTokenFlag)
		if controlToken == "" {
			controlToken, err = controlapi.NewStore(*storeDir).Token()
			if err != nil {
				fatal(fmt.Errorf("load internal control token: %w", err))
			}
		}
		gateway, err := newWeb(*webAddr, *controlAddr, controlToken, *authDir, *allowedHosts, *webAuth, *requireBootstrapToken, *secureCookies, *qnapOnly)
		if err != nil {
			fatal(err)
		}
		errCh := make(chan error, 2)
		if hostManager != nil {
			go hostManager.Run(ctx)
		}
		go func() { errCh <- control.Serve(ctx) }()
		go func() { errCh <- gateway.Serve(ctx) }()
		fmt.Printf("OpenSurge privileged control: http://%s (loopback only)\n", *controlAddr)
		fmt.Printf("OpenSurge Web: http://%s\n", *webAddr)
		var serveErr error
		select {
		case <-ctx.Done():
		case serveErr = <-errCh:
			cancel()
		}
		releaseHostRouting(hostManager)
		if serveErr != nil {
			fatal(serveErr)
		}
	default:
		fatal(fmt.Errorf("unsupported --component %q", *component))
	}
}

func newControl(configPath, storeDir, controlAddr string) (*controlapi.Server, *qnaphost.Manager, error) {
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cleanupCancel()
	if err := qnaphost.CleanupLegacyTrafficScopes(cleanupCtx, storeDir); err != nil {
		fmt.Fprintf(os.Stderr, "warning: clean up removed container traffic takeover: %v\n", err)
	}

	controlToken, err := controlapi.NewStore(storeDir).Token()
	if err != nil {
		return nil, nil, fmt.Errorf("load internal control token: %w", err)
	}
	platform, err := webgateway.ParseNASPlatform(os.Getenv("OPENSURGE_NAS_PLATFORM"))
	if err != nil {
		return nil, nil, err
	}
	var hostManager *qnaphost.Manager
	runner := controlapi.ContainerRunner{StoreDir: storeDir}
	static := webui.Handler()
	if platform == "qnap" {
		hostManager = qnaphost.New(configPath, storeDir)
		static = hostManager.Handler(controlToken, static)
	}
	static = controlapi.WrapCloudflareOptimizer(static, configPath, storeDir, runner)
	static = controlapi.WrapQNAPSourceDelete(static, configPath, storeDir, controlToken)
	control, err := controlapi.New(controlapi.Options{
		ConfigPath:        configPath,
		Addr:              controlAddr,
		StoreDir:          storeDir,
		Runner:            runner,
		DiscoverNetwork:   linuxnetwork.Discover,
		DiscoverDefault:   linuxnetwork.DiscoverDefault,
		ListInterfaces:    linuxnetwork.ListInterfaces,
		DiscoverNeighbors: linuxnetwork.DiscoverNeighbors,
		LookupRoute:       linuxnetwork.LookupRoute,
		PingRouter:        linuxnetwork.PingRouter,
		Static:            static,
	})
	if err != nil {
		return nil, nil, err
	}
	return control, hostManager, nil
}

func newWeb(webAddr, controlAddr, token, authDir, allowedHosts string, webAuth, requireBootstrapToken, secureCookies, qnapOnly bool) (*webgateway.Server, error) {
	return webgateway.New(webgateway.Options{
		Addr:                  webAddr,
		Upstream:              "http://" + controlAddr,
		ControlToken:          token,
		AuthDir:               authDir,
		AllowedHosts:          splitCSV(allowedHosts),
		DisableAuthentication: !webAuth,
		RequireBootstrapToken: requireBootstrapToken,
		SecureCookies:         secureCookies,
		QNAPOnly:              qnapOnly,
		NASPlatform:           os.Getenv("OPENSURGE_NAS_PLATFORM"),
	})
}

func releaseHostRouting(manager *qnaphost.Manager) {
	if manager == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Release(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "release NAS host routing: %v\n", err)
	}
}

func splitCSV(value string) []string {
	var values []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			values = append(values, item)
		}
	}
	return values
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
