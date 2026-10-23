package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/KLORQ/quarshell-core/isolation"
)

func TestSandboxTokenFromEnvironment(t *testing.T) {
	t.Setenv(EnvSupervisorAuthBundleFile, "")
	t.Run("environment value takes precedence", func(t *testing.T) {
		t.Setenv(EnvSandboxToken, " env-token ")
		t.Setenv(EnvSandboxTokenFile, filepath.Join(t.TempDir(), "missing"))

		got, err := sandboxTokenFromEnvironment()
		if err != nil || got != "env-token" {
			t.Fatalf("sandboxTokenFromEnvironment() = %q, %v; want env-token", got, err)
		}
	})

	t.Run("reads and trims file value", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "sandbox-token")
		if err := os.WriteFile(path, []byte("\n file-token \n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv(EnvSandboxToken, "")
		t.Setenv(EnvSandboxTokenFile, path)

		got, err := sandboxTokenFromEnvironment()
		if err != nil || got != "file-token" {
			t.Fatalf("sandboxTokenFromEnvironment() = %q, %v; want file-token", got, err)
		}
	})

	t.Run("no source is optional", func(t *testing.T) {
		t.Setenv(EnvSandboxToken, "")
		t.Setenv(EnvSandboxTokenFile, "")

		got, err := sandboxTokenFromEnvironment()
		if err != nil || got != "" {
			t.Fatalf("sandboxTokenFromEnvironment() = %q, %v; want empty token", got, err)
		}
	})

	t.Run("rejects relative file path", func(t *testing.T) {
		t.Setenv(EnvSandboxToken, "")
		t.Setenv(EnvSandboxTokenFile, "token")

		if _, err := sandboxTokenFromEnvironment(); err == nil {
			t.Fatal("sandboxTokenFromEnvironment() accepted a relative path")
		}
	})

	t.Run("rejects empty file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "sandbox-token")
		if err := os.WriteFile(path, []byte(" \n\t "), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv(EnvSandboxToken, "")
		t.Setenv(EnvSandboxTokenFile, path)

		if _, err := sandboxTokenFromEnvironment(); err == nil {
			t.Fatal("sandboxTokenFromEnvironment() accepted an empty token file")
		}
	})
}

func TestSandboxTokenFromDescriptorBoundAuthBundle(t *testing.T) {
	dir := t.TempDir()
	descriptor := isolation.Descriptor{
		Version: isolation.DescriptorVersion, BackendName: "quarshell/vm", SandboxID: "sandbox-a",
		SessionID: "11111111-1111-4111-8111-111111111111", Generation: "g0000000000000001",
		RootfsDigest:  "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		RuntimeDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Payload:       json.RawMessage(`{"worker_id":"sandbox-a"}`),
	}
	descriptorBytes, err := isolation.EncodeDescriptor(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	descriptorPath := filepath.Join(dir, "descriptor.json")
	if err := os.WriteFile(descriptorPath, descriptorBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	bundleBytes, err := isolation.EncodeHostAuthBundle(isolation.HostAuthBundle{
		SessionID: descriptor.SessionID, RuntimeGeneration: descriptor.Generation,
		SessionRotation: 1, AuthEpoch: 2, GatewayToken: "signed.jwt.value",
		GatewayExpiresAt: time.Now().Add(time.Hour).Unix(), SandboxToken: "sandbox.jwt.value",
		SandboxExpiresAt: time.Now().Add(time.Hour).Unix(),
	}, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	bundlePath := filepath.Join(dir, "auth-bundle.json")
	if err := os.WriteFile(bundlePath, bundleBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvSandboxToken, "")
	t.Setenv(EnvSandboxTokenFile, "")
	t.Setenv(EnvSupervisorAuthBundleFile, bundlePath)
	t.Setenv("QUARSHELL_ISOLATION_DESCRIPTOR_PATH", descriptorPath)
	t.Setenv("QUARSHELL_SANDBOX", descriptor.SandboxID)
	t.Setenv("QUARSHELL_ADMITTED_ISOLATION_BACKEND", descriptor.BackendName)
	got, err := sandboxTokenFromEnvironment()
	if err != nil || got != "signed.jwt.value" {
		t.Fatalf("sandboxTokenFromEnvironment() = %q, %v", got, err)
	}
	got, err = sandboxProtocolTokenFromEnvironment()
	if err != nil || got != "sandbox.jwt.value" {
		t.Fatalf("sandboxProtocolTokenFromEnvironment() = %q, %v; want Sandbox Protocol bearer", got, err)
	}
	t.Setenv("QUARSHELL_SANDBOX", "another-sandbox")
	if _, err := sandboxTokenFromEnvironment(); err == nil {
		t.Fatal("auth bundle for a different sandbox was accepted")
	}
}
