//go:build darwin

package service

import (
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type launchdPlist struct {
	XMLName xml.Name `xml:"plist"`
	Version string   `xml:"version,attr"`
	Dict    launchdDict
}

type launchdDict struct {
	XMLName xml.Name `xml:"dict"`
	Items   []any
}

func startGatewayService(bin string, args, env []string, dataDir, logPath string) (bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, err
	}
	label := "dev.klorq.quarshell.gateway"
	dir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, err
	}
	path := filepath.Join(dir, label+".plist")
	content, err := renderLaunchdPlist(label, bin, args, env, logPath)
	if err != nil {
		return false, err
	}
	if err := writeServiceFile(path, content); err != nil {
		return false, err
	}
	uid := strconv.Itoa(os.Getuid())
	_ = exec.Command("launchctl", "bootout", "gui/"+uid+"/"+label).Run()
	if out, err := exec.Command("launchctl", "bootstrap", "gui/"+uid, path).CombinedOutput(); err != nil {
		return false, fmt.Errorf("launchctl bootstrap: %w: %s", err, strings.TrimSpace(string(out)))
	}
	_ = exec.Command("launchctl", "kickstart", "-k", "gui/"+uid+"/"+label).Run()
	return true, nil
}

func renderLaunchdPlist(label, bin string, args, env []string, logPath string) ([]byte, error) {
	escape := func(value string) string {
		var b strings.Builder
		_ = xml.EscapeText(&b, []byte(value))
		return b.String()
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n")
	b.WriteString("<key>Label</key><string>" + escape(label) + "</string>\n")
	b.WriteString("<key>ProgramArguments</key><array>\n<string>" + escape(bin) + "</string>\n")
	for _, arg := range args {
		b.WriteString("<string>" + escape(arg) + "</string>\n")
	}
	b.WriteString("</array>\n<key>EnvironmentVariables</key><dict>\n")
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		b.WriteString("<key>" + escape(key) + "</key><string>" + escape(value) + "</string>\n")
	}
	b.WriteString("</dict>\n<key>RunAtLoad</key><true/>\n<key>KeepAlive</key><true/>\n")
	b.WriteString("<key>StandardOutPath</key><string>" + escape(logPath) + "</string>\n")
	b.WriteString("<key>StandardErrorPath</key><string>" + escape(logPath) + "</string>\n")
	b.WriteString("</dict>\n</plist>\n")
	return []byte(b.String()), nil
}
