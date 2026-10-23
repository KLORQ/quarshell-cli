package service

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Banner is the short CLI intro.
func (a *App) Banner() string {
	return "cautem — agent sandbox CLI"
}

// Version reports the CLI stub version.
// BuildVersion is set at release time:
// -ldflags "-X github.com/KLORQ/quarshell-cli/internal/service.BuildVersion=…".
var BuildVersion = "0.1.0-alpha.1"

func (a *App) Version() string { return "cautem " + BuildVersion }

// Doctor checks the Docker prerequisite used by the pinned OpenShell CLI.
func (a *App) Doctor() error {
	fmt.Fprint(os.Stdout, "Checking system prerequisites...\n\n")
	fmt.Fprint(os.Stdout, "  Docker ............. ")
	ctx, cancel := a.withTimeout(TimeoutAPI)
	defer cancel()
	command := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}")
	output, err := command.CombinedOutput()
	if err != nil {
		fmt.Fprint(os.Stdout, "FAILED\n\n")
		if len(output) == 0 {
			return fmt.Errorf("doctor: failed to execute docker info: %w", err)
		}
		return fmt.Errorf("doctor: docker info failed: %s", strings.TrimSpace(string(output)))
	}
	version := strings.TrimSpace(string(output))
	fmt.Fprintf(os.Stdout, "ok (version %s)\n", version)
	fmt.Fprint(os.Stdout, "  DOCKER_HOST ........ ")
	if value, ok := os.LookupEnv("DOCKER_HOST"); ok {
		fmt.Fprintln(os.Stdout, value)
	} else {
		fmt.Fprintln(os.Stdout, "(not set, using default socket)")
	}
	fmt.Fprintln(os.Stdout, "\nAll checks passed.")
	return nil
}
