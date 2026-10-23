//go:build !windows

package sshconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallRestrictsManagedFilePermissions(t *testing.T) {
	path := Paths{
		Managed: filepath.Join(t.TempDir(), "managed", "ssh_config"),
		User:    filepath.Join(t.TempDir(), "ssh", "config"),
	}
	if err := Install(path, "quarshell-demo", RenderHostBlock("quarshell-demo", "w ssh-proxy --name demo")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path.Managed)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("managed mode %o, want 600", got)
	}
}
