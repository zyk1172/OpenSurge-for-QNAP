package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"open-mihomo-gateway/internal/controlapi"
	"open-mihomo-gateway/internal/deploymentweb"
	"open-mihomo-gateway/internal/webgateway"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	storeDir := getenv("OPENSURGE_MANAGER_STORE", "/data/control")
	controlAddr := getenv("OPENSURGE_MANAGER_CONTROL_ADDR", "127.0.0.1:61779")
	webAddr := getenv("OPENSURGE_MANAGER_WEB_ADDR", "0.0.0.0:61780")
	orchestratorSocket := getenv("OPENSURGE_ORCHESTRATOR_SOCKET", "/run/opensurge/orchestrator.sock")

	token, err := controlapi.NewStore(storeDir).Token()
	if err != nil {
		fatal(fmt.Errorf("load deployment control token: %w", err))
	}
	control, err := deploymentweb.New(deploymentweb.Options{
		Addr: controlAddr,
		Token: token,
		OrchestratorSocket: orchestratorSocket,
	})
	if err != nil {
		fatal(err)
	}
	gateway, err := webgateway.New(webgateway.Options{
		Addr: webAddr,
		Upstream: "http://"+controlAddr,
		ControlToken: token,
		AuthDir: storeDir,
		AllowedHosts: splitCSV(os.Getenv("OPENSURGE_MANAGER_ALLOWED_HOSTS")),
	})
	if err != nil {
		fatal(err)
	}

	errCh := make(chan error, 2)
	go func() { errCh <- control.Serve(ctx) }()
	go func() { errCh <- gateway.Serve(ctx) }()
	fmt.Printf("OpenSurge deployment manager: http://%s\n", webAddr)

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

func getenv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
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
