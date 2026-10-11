//go:build !linux

package runtimeimage

import "fmt"

func Mount(string) error { return fmt.Errorf("runtime image cache requires Linux Docker") }
