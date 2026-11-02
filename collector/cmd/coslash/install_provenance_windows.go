//go:build windows

package main

func trustedCustomScriptInstall(_, _ string) bool { return false }
