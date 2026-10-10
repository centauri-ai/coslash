//go:build !linux

package main

import "io"

func reportBackgroundPersistence(io.Writer) {}
