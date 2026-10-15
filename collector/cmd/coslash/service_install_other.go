//go:build !darwin

package main

import "fmt"

func installService() error { return fmt.Errorf("service install is only available on macOS") }
