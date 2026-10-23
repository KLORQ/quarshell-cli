package service

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/KLORQ/quarshell-core/defaults"
	"github.com/KLORQ/quarshell-core/slogx"
)

const localGatewayName = "local"
const localGatewayURL = "https://127.0.0.1:17670"
const localGatewayGRPCEndpoint = "127.0.0.1:17671"

// GatewayEnsure makes sure a reachable gateway is selected (OpenShell install UX).
// If the current gateway is healthy, it is a no-op. Otherwise it starts a local
// quarshell-gateway on 127.0.0.1:7443 (sibling binary or PATH), registers it as "local",
// and selects it.
func (a *App) GatewayEnsure() error {
	const op = "cli.gateway.ensure"
	log := a.op(op)
	if u, err := a.currentGatewayURL(); err == nil && u != "" {
		ctx, cancel := a.withTimeout(TimeoutProbe)
		defer cancel()
		if _, err := a.clientFor(u).Healthz(ctx); err == nil {
			log.Info("gateway already healthy", slog.String("url", u))
			return nil
		}
	}
	// Prefer already-listening local port (user started gateway manually).
	if portOpen("127.0.0.1", 17670) {
		_ = a.GatewayAdd(localGatewayName, localGatewayURL)
		_ = a.GatewaySelect(localGatewayName)
		ctx, cancel := a.withTimeout(TimeoutProbe)
		defer cancel()
		if _, err := a.clientFor(localGatewayURL).Healthz(ctx); err == nil {
			log.Info("using existing local gateway", slog.String("url", localGatewayURL))
			fmt.Printf("gateway ensure: using existing %s\n", localGatewayURL)
			return nil
		}
	}
	bin, err := findGatewayBinary()
	if err != nil {
		log.Error("gateway binary not found", slogx.Err(err))
		return fmt.Errorf("gateway ensure: %w\nStart manually: quarshell-gateway --listen %s\nThen: quarshell gateway add local --url %s && quarshell gateway select local",
			err, defaults.GatewayListen, localGatewayURL)
	}
	logPath, err := gatewayLogPath()
	if err != nil {
		log.Error("failed to resolve gateway log path", slogx.Err(err))
		return err
	}
	logF, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		log.Error("failed to open gateway log", slogx.Err(err))
		return fmt.Errorf("gateway ensure: log: %w", err)
	}
	dataDir := defaultGatewayDataDir()
	token, err := ensureGatewayToken(dataDir)
	if err != nil {
		_ = logF.Close()
		return fmt.Errorf("gateway ensure: token: %w", err)
	}
	log.Info("starting local gateway", slog.String("bin", bin), slog.String("listen", "127.0.0.1:17670"))
	tlsFiles, err := ensureGatewayTLS(dataDir)
	if err != nil {
		_ = logF.Close()
		return fmt.Errorf("gateway ensure: TLS: %w", err)
	}
	gatewayArgs := []string{"--listen", "127.0.0.1:17670", "--grpc-listen", localGatewayGRPCEndpoint, "--data-dir", dataDir, "--tls-cert", tlsFiles.ServerCert, "--tls-key", tlsFiles.ServerKey, "--tls-client-ca", tlsFiles.CA}
	runtimeName := strings.TrimSpace(os.Getenv("QUARSHELL_GATEWAY_RUNTIME"))
	if runtimeName != "" {
		gatewayArgs = append(gatewayArgs, "--runtime", runtimeName)
	}
	if runtimeConfig := strings.TrimSpace(os.Getenv("QUARSHELL_GATEWAY_RUNTIME_CONFIG")); runtimeConfig != "" {
		gatewayArgs = append(gatewayArgs, "--runtime-config", runtimeConfig)
	}
	gatewayEnv := []string{"QUARSHELL_TOKEN=" + token}
	if runtimeName == "vm" {
		if os.Getenv("OPENSHELL_VM_TLS_CA") == "" {
			gatewayEnv = append(gatewayEnv, "OPENSHELL_VM_TLS_CA="+tlsFiles.CA)
		}
		if os.Getenv("OPENSHELL_GRPC_ENDPOINT") == "" {
			gatewayEnv = append(gatewayEnv, "OPENSHELL_GRPC_ENDPOINT=https://127.0.0.1:17671")
		}
		gatewayEnv = append(gatewayEnv,
			"QUARSHELL_GUEST_TLS_CA="+tlsFiles.CA,
			"QUARSHELL_GUEST_TLS_CERT="+tlsFiles.ClientCert,
			"QUARSHELL_GUEST_TLS_KEY="+tlsFiles.ClientKey,
		)
	}
	serviceStarted := false
	if os.Getenv("QUARSHELL_GATEWAY_SERVICE") != "0" {
		serviceStarted, _ = startGatewayService(bin, gatewayArgs, gatewayEnv, dataDir, logPath)
	}
	pid := 0
	if serviceStarted {
		_ = logF.Close()
	} else {
		cmd := exec.Command(bin, gatewayArgs...)
		cmd.Env = append(os.Environ(), gatewayEnv...)
		cmd.Stdout = logF
		cmd.Stderr = logF
		cmd.SysProcAttr = gatewaySysProcAttr()
		if err := cmd.Start(); err != nil {
			_ = logF.Close()
			log.Error("failed to start gateway", slogx.Err(err))
			return fmt.Errorf("gateway ensure: start %s: %w", bin, err)
		}
		pid = cmd.Process.Pid
		// Detach: do not wait; log file stays open in child.
		go func() {
			_ = cmd.Wait()
			_ = logF.Close()
		}()
	}
	deadline := time.Now().Add(gatewayStartupTimeout)
	for time.Now().Before(deadline) {
		ctx, cancel := a.withTimeout(TimeoutProbeFast)
		_, err := a.clientFor(localGatewayURL).Healthz(ctx)
		cancel()
		if err == nil {
			_ = a.GatewayAdd(localGatewayName, localGatewayURL)
			_ = a.GatewaySelect(localGatewayName)
			log.Info("local gateway ready", slog.Int("pid", pid), slog.String("log", logPath))
			fmt.Printf("gateway ensure: started %s (pid %d, log %s)\n", localGatewayURL, pid, logPath)
			return nil
		}
		time.Sleep(gatewayStartupPollInterval)
	}
	err = fmt.Errorf("gateway ensure: started %s but healthz not ready (see %s)", bin, logPath)
	log.Error("gateway healthz timeout", slogx.Err(err))
	return err
}

func ensureGatewayToken(dataDir string) (string, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dataDir, gatewayAuthTokenFile)
	if data, err := os.ReadFile(path); err == nil {
		if token := strings.TrimSpace(string(data)); token != "" {
			return token, nil
		}
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw[:])
	tmp, err := os.CreateTemp(dataDir, "auth-token-*")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if _, err := tmp.WriteString(token); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(name, path); err != nil {
		return "", err
	}
	return token, nil
}

func portOpen(host string, port int) bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), TimeoutDial)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func findGatewayBinary() (string, error) {
	candidates := []string{}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, "quarshell-gateway"),
			filepath.Join(dir, "quarshell-gateway-darwin-arm64"),
			filepath.Join(dir, "quarshell-gateway-darwin-amd64"),
			filepath.Join(dir, "quarshell-gateway-linux-arm64"),
			filepath.Join(dir, "quarshell-gateway-linux-amd64"),
		)
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(wd, "quarshell-gateway"),
			filepath.Join(wd, "quarshell-cli", "quarshell-gateway"),
			filepath.Join(wd, "quarshell-gateway", "quarshell-gateway"),
		)
	}
	if p, err := exec.LookPath("quarshell-gateway"); err == nil {
		candidates = append(candidates, p)
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() && st.Mode().Perm()&0o111 != 0 {
			return c, nil
		}
	}
	return "", fmt.Errorf("quarshell-gateway binary not found next to quarshell or on PATH")
}

func gatewayLogPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	dir = filepath.Join(dir, "quarshell")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "gateway.log"), nil
}

// InferProvidersFromArgv maps trailing create commands to provider profile ids
// (OpenShell detect_provider_from_command).
func InferProvidersFromArgv(argv []string) []string {
	if len(argv) == 0 {
		return nil
	}
	base := strings.ToLower(filepath.Base(argv[0]))
	base = strings.TrimSuffix(base, ".exe")
	switch base {
	case "agent", "cursor-agent", "cursor":
		return []string{"cursor"}
	case "claude", "claude-code":
		return []string{"claude-code"}
	case "codex":
		return []string{"codex"}
	case "gh", "git":
		return []string{"github"}
	default:
		return nil
	}
}
