//go:build !darwin && !linux

package service

func startGatewayService(string, []string, []string, string, string) (bool, error) { return false, nil }
