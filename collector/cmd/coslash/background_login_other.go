//go:build !darwin && !windows && !linux

package main

func registerBackgroundLogin() error { return nil }

func backgroundLoginLoaded() bool { return false }
