package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"open-mihomo-gateway/internal/deployment"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	socket := getenv("OPENSURGE_ORCHESTRATOR_SOCKET", "/run/opensurge/orchestrator.sock")
	dockerSocket := getenv("OPENSURGE_DOCKER_SOCKET", "/var/run/docker.sock")
	image := getenv("OPENSURGE_GATEWAY_IMAGE", deployment.DefaultGatewayImage)

	client := deployment.NewDockerClient(dockerSocket)
	orchestrator := &deployment.Orchestrator{Docker: client, Image: image}
	if err := client.Ping(ctx); err != nil {
		fatal(err)
	}
	fmt.Printf("OpenSurge orchestrator listening on unix://%s\n", socket)
	if err := deployment.ServeRPC(ctx, socket, orchestrator); err != nil {
		fatal(err)
	}
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
