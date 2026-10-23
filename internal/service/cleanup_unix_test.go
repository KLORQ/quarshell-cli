//go:build !windows

package service

func dockerTestExecutableName() string { return "docker" }
