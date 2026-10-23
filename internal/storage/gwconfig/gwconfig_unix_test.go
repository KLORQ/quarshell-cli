//go:build !windows

package gwconfig

import (
	"os"
	"testing"
)

func TestSaveRestrictsTokenFilePermissions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := Save(File{Current: "dev", Gateways: map[string]Gateway{"dev": {URL: "http://127.0.0.1:7443", Token: "secret"}}}); err != nil {
		t.Fatal(err)
	}
	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config with tokens has mode %o, want 600", got)
	}
}
