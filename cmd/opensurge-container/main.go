package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"open-mihomo-gateway/internal/controlapi"
	"open-mihomo-gateway/internal/linuxnetwork"
	"open-mihomo-gateway/internal/qnaphost"
	"open-mihomo-gateway/internal/qnapsetup"
	"open-mihomo-gateway/internal/webgateway"
	"open-mihomo-gateway/internal/webui"
)

func main() {
	configPath := flag.String("config", "/data/config/opensurge.yaml", "path to persistent gateway config")
	storeDir := flag.String("store", "/data/control", "persistent control/auth directory")
	controlAddr := flag.String("control-addr", "127.0.0.1:61767", "loopback-only privileged Control API address")
	webAddr := flag.String("web-addr", "0.0.0.0:8080", "LAN-facing authenticated Web address")
	allowedHosts := flag.String("allowed-hosts", "", "comma-separated additional hostnames accepted by the Web gateway")
	hostAgentSocket := flag.String("host-agent-socket", "/run/opensurge-host/agent.sock", "QNAP host-agent unix socket")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	control, err := controlapi.New(controlapi.Options{
		ConfigPath:        *configPath,
		Addr:              *controlAddr,
		StoreDir:          *storeDir,
		Runner:            controlapi.ContainerRunner{},
		DiscoverNetwork:   linuxnetwork.Discover,
		DiscoverDefault:   linuxnetwork.DiscoverDefault,
		ListInterfaces:    linuxnetwork.ListInterfaces,
		DiscoverNeighbors: linuxnetwork.DiscoverNeighbors,
		LookupRoute:       linuxnetwork.LookupRoute,
		PingRouter:        linuxnetwork.PingRouter,
		Static:            webui.Handler(),
	})
	if err != nil {
		fatal(err)
	}
	controlToken, err := controlapi.NewStore(*storeDir).Token()
	if err != nil {
		fatal(fmt.Errorf("load internal control token: %w", err))
	}
	gateway, err := webgateway.New(webgateway.Options{
		Addr:         *webAddr,
		Upstream:     "http://" + *controlAddr,
		ControlToken: controlToken,
		AuthDir:      *storeDir,
		AllowedHosts: splitCSV(*allowedHosts),
	})
	if err != nil {
		fatal(err)
	}

	hostClient := qnaphost.NewClient(*hostAgentSocket)
	qnapSetup := qnapsetup.Handler{ConfigPath: *configPath, Agent: hostClient}
	privateRoutes := map[string]http.Handler{
		"/api/qnap/": qnapSetup,
	}

	errCh := make(chan error, 2)
	go func() { errCh <- control.Serve(ctx) }()
	go func() { errCh <- gateway.ServeWithPrivateRoutes(ctx, privateRoutes) }()
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
