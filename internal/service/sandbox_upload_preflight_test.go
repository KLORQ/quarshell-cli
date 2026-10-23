package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSandboxCreateRejectsEmptyGitUploadBeforeProvisioning(t *testing.T) {
	repo := t.TempDir()
	gitTestCommand(t, repo, "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("src/*.ignored\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(repo, "src")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "file.ignored"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := &App{}

	err := app.SandboxCreate(SandboxCreateOpts{Name: "upload", Image: "alpine:3.22", Upload: source})
	if err == nil || !strings.Contains(err.Error(), "selected no files") {
		t.Fatalf("SandboxCreate() error=%v; want Git preflight rejection", err)
	}
}

func TestSandboxCreateUploadDestinationMatchesSourceType(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "source.txt")
	if err := os.WriteFile(file, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "source-link")
	if err := os.Symlink("source.txt", link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}

	for _, test := range []struct {
		name, staged, original, want string
	}{
		{name: "directory", staged: dir, original: dir, want: "/workspace"},
		{name: "file", staged: file, original: file, want: "/workspace/source.txt"},
		{name: "symlink", staged: link, original: link, want: "/workspace/source-link"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := sandboxCreateUploadDestination(test.staged, test.original)
			if err != nil || got != test.want {
				t.Fatalf("sandboxCreateUploadDestination()=(%q,%v); want %q", got, err, test.want)
			}
		})
	}
}

func gitTestCommand(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}
