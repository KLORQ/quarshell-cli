package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveRootfsTarSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rootfs.TAR.GZ")
	if err := os.WriteFile(path, []byte("tar bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, isTar, err := resolveRootfsTarSource(path)
	if err != nil || !isTar || got != path {
		t.Fatalf("resolveRootfsTarSource() = (%q, %v, %v)", got, isTar, err)
	}
	if _, _, err := resolveRootfsTarSource(filepath.Join(t.TempDir(), "missing.tar")); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing tar error=%v", err)
	}
	if _, _, err := resolveRootfsTarSource(t.TempDir()); err == nil || !strings.Contains(err.Error(), "local directories") {
		t.Fatalf("directory source error=%v", err)
	}
	if _, _, err := resolveRootfsTarSource(filepath.Join(t.TempDir(), "image.img")); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing local image path error=%v", err)
	}
	if _, isTar, err := resolveRootfsTarSource("registry.example.test/team/rootfs.tar"); err != nil || isTar {
		t.Fatalf("registry image ref classification = (isTar=%v, err=%v)", isTar, err)
	}
}

func TestIsLoopbackGatewayEndpoint(t *testing.T) {
	for _, endpoint := range []string{"http://127.0.0.1:7443", "https://[::1]:7443", "http://localhost:7443"} {
		if !isLoopbackGatewayEndpoint(endpoint) {
			t.Errorf("%q should be loopback", endpoint)
		}
	}
	for _, endpoint := range []string{"https://gateway.example.test", "http://192.0.2.1:7443", "bad"} {
		if isLoopbackGatewayEndpoint(endpoint) {
			t.Errorf("%q should not be loopback", endpoint)
		}
	}
}

func TestMergeRootfsTarDriverConfig(t *testing.T) {
	got, err := mergeRootfsTarDriverConfig(`{"docker":{"network":"isolated"},"vm":{"cpus":2}}`, "single-use-token")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"rootfs_tar_staging_token":"single-use-token"`) || !strings.Contains(got, `"cpus":2`) || !strings.Contains(got, `"network":"isolated"`) {
		t.Fatalf("merged driver config lost values: %s", got)
	}
	if _, err := mergeRootfsTarDriverConfig(`{"vm":{"rootfs_tar_staging_token":"caller"}}`, "server"); err == nil {
		t.Fatal("caller-controlled staging token was accepted")
	}
	if _, err := mergeRootfsTarDriverConfig(`{"vm":false}`, "server"); err == nil {
		t.Fatal("non-object VM config was accepted")
	}
}
