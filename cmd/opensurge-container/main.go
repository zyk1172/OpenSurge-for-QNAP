package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"open-mihomo-gateway/internal/controlapi"
	"open-mihomo-gateway/internal/linuxnetwork"
	"open-mihomo-gateway/internal/webgateway"
	"open-mihomo-gateway/internal/webui"
)

func main() {
	component := flag.String("component", "all", "component to run: all, control, web, or token")
	configPath := flag.String("config", "/data/config/opensurge.yaml", "path to persistent gateway config")
	storeDir := flag.String("store", "/data/control", "persistent privileged control directory")
	authDir := flag.String("auth-dir", "", "persistent Web authentication directory")
	controlAddr := flag.String("control-addr", "127.0.0.1:61767", "loopback-only privileged Control API address")
	webAddr := flag.String("web-addr", "0.0.0.0:8080", "LAN-facing authenticated Web address")
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
	case "control":
		control, err := newControl(*configPath, *storeDir, *controlAddr)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("OpenSurge privileged control: http://%s (loopback only)\n", *controlAddr)
		if err := control.Serve(ctx); err != nil {
			fatal(err)
		}
	case "web":
		if strings.TrimSpace(*controlTokenFlag) == "" {
			fatal(fmt.Errorf("--control-token is required for the standalone Web component"))
		}
		gateway, err := newWeb(*webAddr, *controlAddr, *controlTokenFlag, *authDir, *allowedHosts, *requireBootstrapToken, *secureCookies, *qnapOnly)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("OpenSurge Web: http://%s\n", *webAddr)
		if err := gateway.Serve(ctx); err != nil {
			fatal(err)
		}
	case "all":
		control, err := newControl(*configPath, *storeDir, *controlAddr)
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
		gateway, err := newWeb(*webAddr, *controlAddr, controlToken, *authDir, *allowedHosts, *requireBootstrapToken, *secureCookies, *qnapOnly)
		if err != nil {
			fatal(err)
		}
		errCh := make(chan error, 2)
		go func() { errCh <- control.Serve(ctx) }()
		go func() { errCh <- gateway.Serve(ctx) }()
		fmt.Printf("OpenSurge privileged control: http://%s (loopback only)\n", *controlAddr)
		fmt.Printf("OpenSurge Web: http://%s\n", *webAddr)
		select {
		case <-ctx.Done():
			return
		case err := <-errCh:
			if err != nil {
				cancel()
				fatal(err)
			}
		}
	default:
		fatal(fmt.Errorf("unsupported --component %q", *component))
	}
}

func newControl(configPath, storeDir, controlAddr string) (*controlapi.Server, error) {
	return controlapi.New(controlapi.Options{
		ConfigPath:        configPath,
		Addr:              controlAddr,
		StoreDir:          storeDir,
		Runner:            controlapi.ContainerRunner{},
		DiscoverNetwork:   linuxnetwork.Discover,
		DiscoverDefault:   linuxnetwork.DiscoverDefault,
		ListInterfaces:    linuxnetwork.ListInterfaces,
		DiscoverNeighbors: linuxnetwork.DiscoverNeighbors,
		LookupRoute:       linuxnetwork.LookupRoute,
		PingRouter:        linuxnetwork.PingRouter,
		Static:            webui.Handler(),
	})
}

func newWeb(webAddr, controlAddr, token, authDir, allowedHosts string, requireBootstrapToken, secureCookies, qnapOnly bool) (*webgateway.Server, error) {
	return webgateway.New(webgateway.Options{
		Addr:                  webAddr,
		Upstream:              "http://" + controlAddr,
		ControlToken:          token,
		AuthDir:               authDir,
		AllowedHosts:          splitCSV(allowedHosts),
		RequireBootstrapToken: requireBootstrapToken,
		SecureCookies:         secureCookies,
		QNAPOnly:              qnapOnly,
	})
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
