//go:build linux

package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func startGatewayService(bin string, args, env []string, dataDir, logPath string) (bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, err
	}
	dir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, err
	}
	path := filepath.Join(dir, "quarshell-gateway.service")
	var b strings.Builder
	b.WriteString("[Unit]\nDescription=Quarshell local gateway\nAfter=network.target\n\n")
	b.WriteString("[Service]\nType=simple\n")
	quoted := make([]string, 0, len(args)+1)
	for _, value := range append([]string{bin}, args...) {
		quoted = append(quoted, shellQuote(value))
	}
	b.WriteString("ExecStart=" + strings.Join(quoted, " ") + "\n")
	for _, item := range env {
		if key, value, ok := strings.Cut(item, "="); ok {
			b.WriteString("Environment=" + shellQuote(key+"="+value) + "\n")
		}
	}
	b.WriteString("Restart=on-failure\nRestartSec=2\nStandardOutput=append:" + logPath + "\nStandardError=append:" + logPath + "\n\n[Install]\nWantedBy=default.target\n")
	if err := writeServiceFile(path, []byte(b.String())); err != nil {
		return false, err
	}
	if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
		return false, fmt.Errorf("systemctl daemon-reload: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("systemctl", "--user", "enable", "--now", "quarshell-gateway.service").CombinedOutput(); err != nil {
		return false, fmt.Errorf("systemctl enable: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return true, nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
