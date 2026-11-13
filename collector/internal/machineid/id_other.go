//go:build !darwin && !linux && !windows

package machineid

func Current() string { return "" }
