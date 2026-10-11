package main

import (
	"encoding/json"
	"fmt"
	"open-mihomo-gateway/internal/runtimeimage"
	"os"
)

func run() error {
	if len(os.Args) == 2 && os.Args[1] == "manifest" {
		m, err := runtimeimage.Generate("/usr")
		if err != nil {
			return err
		}
		data, err := json.Marshal(m)
		if err != nil {
			return err
		}
		if err := os.MkdirAll("/opt/opensurge", 0755); err != nil {
			return err
		}
		return os.WriteFile(runtimeimage.ManifestPath, data, 0644)
	}
	if len(os.Args) != 2 || os.Args[1] != "prepare" && os.Args[1] != "prepare-files" {
		return fmt.Errorf("usage: opensurge-runtime-cache manifest|prepare|prepare-files")
	}
	data, err := os.ReadFile(runtimeimage.ManifestPath)
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "OpenSurge: verifying version-specific runtime image on /data; initial copy may take time")
	root, err := runtimeimage.Prepare("/usr", "/data/runtime-image-cache", data)
	if err != nil {
		return err
	}
	if os.Args[1] == "prepare-files" {
		fmt.Println(root)
		return nil
	}
	if err := runtimeimage.Mount(root); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "OpenSurge: verified runtime /usr mounted read-only from %s\n", root)
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
