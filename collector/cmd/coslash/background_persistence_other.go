//go:build !linux

package main

import "io"

func startSupervisedBackground() bool { return false }

func reportBackgroundPersistence(io.Writer) {}
