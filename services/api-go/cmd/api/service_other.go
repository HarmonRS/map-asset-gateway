//go:build !windows

package main

// Service management is Windows-only; other platforms run the console binary.
func handleServiceArgs(command, envFile string) (bool, error) { return false, nil }

func runAsWindowsService() bool { return false }
