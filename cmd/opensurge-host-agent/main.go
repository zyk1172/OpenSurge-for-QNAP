package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"open-mihomo-gateway/internal/qnaphost"
)

func main() {
	socketPath := flag.String("socket", "/run/opensurge-host/agent.sock", "unix socket exposed to the OpenSurge control plane")
	dockerSocket := flag.String("docker-socket", "/var/run/docker.sock", "QNAP Docker Engine unix socket")
	containerName := flag.String("container", "opensurge", "managed OpenSurge container name")
	networkName := flag.String("network", "opensurge-qnet", "managed OpenSurge QNET name")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	server := qnaphost.NewServer(*socketPath, *dockerSocket, *containerName, *networkName)
	if err := server.Serve(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
