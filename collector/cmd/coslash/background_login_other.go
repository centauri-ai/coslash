//go:build !darwin && !windows

package main

func registerBackgroundLogin() error { return nil }

func backgroundLoginLoaded() bool { return false }
