package service

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const (
	vmDriverConfigKey   = "vm"
	rootfsTarTokenField = "rootfs_tar_staging_token"
)

func resolveRootfsTarSource(value string) (string, bool, error) {
	if strings.TrimSpace(value) == "" {
		return "", false, nil
	}
	lower := strings.ToLower(value)
	isTar := strings.HasSuffix(lower, ".tar") || strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz")
	info, err := os.Stat(value)
	if err == nil && info.IsDir() {
		return "", false, fmt.Errorf("local directories are not image sources; build and publish an image or upload files after create")
	}
	if !isTar {
		if looksLikeExplicitLocalPath(value) && os.IsNotExist(err) {
			return "", false, fmt.Errorf("local --from path does not exist: %s", value)
		}
		if looksLikeExplicitLocalPath(value) && err == nil {
			return "", false, fmt.Errorf("local --from files must be rootfs .tar, .tar.gz, or .tgz archives")
		}
		return "", false, nil
	}
	if os.IsNotExist(err) && !looksLikeExplicitLocalPath(value) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("local rootfs tar does not exist: %s", value)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return "", false, fmt.Errorf("rootfs tar must be a non-empty regular file: %s", value)
	}
	path, err := filepath.Abs(value)
	if err != nil {
		return "", false, fmt.Errorf("resolve rootfs tar %q: %w", value, err)
	}
	return path, true, nil
}

func looksLikeExplicitLocalPath(value string) bool {
	return filepath.IsAbs(value) || value == "." || value == ".." || strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../") || strings.HasPrefix(value, "~/")
}

func isLoopbackGatewayEndpoint(endpoint string) bool {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Hostname() == "" {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func mergeRootfsTarDriverConfig(raw, token string) (string, error) {
	config := map[string]json.RawMessage{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &config); err != nil || config == nil {
			return "", fmt.Errorf("sandbox create --driver-config-json must be an object keyed by compute driver")
		}
	}
	vmConfig := map[string]json.RawMessage{}
	if value, ok := config[vmDriverConfigKey]; ok {
		if err := json.Unmarshal(value, &vmConfig); err != nil || vmConfig == nil {
			return "", fmt.Errorf("sandbox create: --driver-config-json %q must be an object", vmDriverConfigKey)
		}
	}
	if _, exists := vmConfig[rootfsTarTokenField]; exists {
		return "", fmt.Errorf("sandbox create: --driver-config-json already sets %s.%s; remove it when using a rootfs tar source", vmDriverConfigKey, rootfsTarTokenField)
	}
	encodedToken, err := json.Marshal(token)
	if err != nil {
		return "", fmt.Errorf("encode rootfs staging token: %w", err)
	}
	vmConfig[rootfsTarTokenField] = encodedToken
	encodedVM, err := json.Marshal(vmConfig)
	if err != nil {
		return "", fmt.Errorf("encode VM driver config: %w", err)
	}
	config[vmDriverConfigKey] = encodedVM
	encoded, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("encode rootfs driver config: %w", err)
	}
	return string(encoded), nil
}
