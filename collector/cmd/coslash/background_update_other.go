//go:build !linux

package main

func backgroundLoginLoadedForTarget(_ string) bool { return backgroundLoginLoaded() }
