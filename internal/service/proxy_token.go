package service

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/KLORQ/quarshell-core/isolation"
)

func sandboxTokenFromEnvironment() (string, error) {
	bundlePath := strings.TrimSpace(os.Getenv(EnvSupervisorAuthBundleFile))
	if bundlePath != "" {
		if strings.TrimSpace(os.Getenv(EnvSandboxToken)) != "" || strings.TrimSpace(os.Getenv(EnvSandboxTokenFile)) != "" {
			return "", fmt.Errorf("proxy: configure one supervisor credential source")
		}
		bundle, err := readSupervisorAuthBundle(bundlePath)
		return bundle.GatewayToken, err
	}
	if token := strings.TrimSpace(os.Getenv(EnvSandboxToken)); token != "" {
		return token, nil
	}

	path := strings.TrimSpace(os.Getenv(EnvSandboxTokenFile))
	if path == "" {
		return "", nil
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", fmt.Errorf("proxy: %s must be a clean absolute path", EnvSandboxTokenFile)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("proxy: read sandbox token file: %w", err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf("proxy: sandbox token file is empty")
	}
	return token, nil
}

func sandboxProtocolTokenFromEnvironment() (string, error) {
	bundlePath := strings.TrimSpace(os.Getenv(EnvSupervisorAuthBundleFile))
	if bundlePath != "" {
		if strings.TrimSpace(os.Getenv(EnvSandboxToken)) != "" || strings.TrimSpace(os.Getenv(EnvSandboxTokenFile)) != "" || strings.TrimSpace(os.Getenv(EnvSandboxProtocolToken)) != "" {
			return "", fmt.Errorf("proxy: configure one supervisor credential source")
		}
		bundle, err := readSupervisorAuthBundle(bundlePath)
		return bundle.SandboxToken, err
	}
	if token := strings.TrimSpace(os.Getenv(EnvSandboxProtocolToken)); token != "" {
		return token, nil
	}
	// Older single-token runtimes use one bearer for both protocol audiences.
	return sandboxTokenFromEnvironment()
}

func readSupervisorAuthBundle(path string) (isolation.HostAuthBundle, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return isolation.HostAuthBundle{}, fmt.Errorf("proxy: %s must be a clean absolute path", EnvSupervisorAuthBundleFile)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 || info.Size() <= 0 || info.Size() > isolation.MaxAuthBundleBytes {
		return isolation.HostAuthBundle{}, fmt.Errorf("proxy: supervisor auth bundle must be a private regular mode-0600 file")
	}
	file, err := os.Open(path)
	if err != nil {
		return isolation.HostAuthBundle{}, fmt.Errorf("proxy: open supervisor auth bundle: %w", err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() || opened.Mode().Perm() != 0o600 {
		return isolation.HostAuthBundle{}, fmt.Errorf("proxy: supervisor auth bundle changed while being opened")
	}
	data, err := io.ReadAll(io.LimitReader(file, isolation.MaxAuthBundleBytes+1))
	if err != nil || len(data) > isolation.MaxAuthBundleBytes {
		return isolation.HostAuthBundle{}, fmt.Errorf("proxy: read supervisor auth bundle: invalid size or read failure")
	}
	descriptorPath := strings.TrimSpace(os.Getenv("CAUTEM_ISOLATION_DESCRIPTOR_PATH"))
	if !filepath.IsAbs(descriptorPath) || filepath.Clean(descriptorPath) != descriptorPath {
		return isolation.HostAuthBundle{}, fmt.Errorf("proxy: supervisor auth bundle requires a clean isolation descriptor path")
	}
	descriptorInfo, err := os.Lstat(descriptorPath)
	if err != nil || !descriptorInfo.Mode().IsRegular() || descriptorInfo.Mode()&os.ModeSymlink != 0 || descriptorInfo.Mode().Perm() != 0o600 || descriptorInfo.Size() <= 0 || descriptorInfo.Size() > isolation.MaxDescriptorBytes {
		return isolation.HostAuthBundle{}, fmt.Errorf("proxy: isolation descriptor must be a private regular mode-0600 file")
	}
	descriptorFile, err := os.Open(descriptorPath)
	if err != nil {
		return isolation.HostAuthBundle{}, fmt.Errorf("proxy: open isolation descriptor: %w", err)
	}
	defer descriptorFile.Close()
	descriptorOpened, err := descriptorFile.Stat()
	if err != nil || !os.SameFile(descriptorInfo, descriptorOpened) || descriptorOpened.Mode().Perm() != 0o600 {
		return isolation.HostAuthBundle{}, fmt.Errorf("proxy: isolation descriptor changed while being opened")
	}
	descriptorData, err := io.ReadAll(io.LimitReader(descriptorFile, isolation.MaxDescriptorBytes+1))
	if err != nil || len(descriptorData) > isolation.MaxDescriptorBytes {
		return isolation.HostAuthBundle{}, fmt.Errorf("proxy: read isolation descriptor: invalid size or read failure")
	}
	descriptor, err := isolation.DecodeDescriptor(descriptorData)
	if err != nil {
		return isolation.HostAuthBundle{}, fmt.Errorf("proxy: decode isolation descriptor: %w", err)
	}
	bundle, err := isolation.DecodeHostAuthBundle(data, descriptor)
	if err != nil {
		return isolation.HostAuthBundle{}, fmt.Errorf("proxy: validate supervisor auth bundle: %w", err)
	}
	if descriptor.SandboxID != strings.TrimSpace(os.Getenv("CAUTEM_SANDBOX")) || descriptor.BackendName != strings.TrimSpace(os.Getenv("CAUTEM_ADMITTED_ISOLATION_BACKEND")) {
		return isolation.HostAuthBundle{}, fmt.Errorf("proxy: supervisor auth bundle does not match sandbox admission")
	}
	return bundle, nil
}
