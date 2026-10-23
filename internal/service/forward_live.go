package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/KLORQ/quarshell-sdk/go/klorq"
)

// ParseForwardBind splits OpenShell `[bind:]port` (default bind 127.0.0.1).
func ParseForwardBind(spec string) (bind string, port int, err error) {
	return parseForwardBind(spec, false)
}

func parseForwardBind(spec string, allowZero bool) (bind string, port int, err error) {
	spec = strings.TrimSpace(spec)
	bind = "127.0.0.1"
	portStr := spec
	if i := strings.LastIndex(spec, ":"); i >= 0 {
		bind = strings.Trim(spec[:i], "[]")
		portStr = spec[i+1:]
		if bind == "" {
			bind = "127.0.0.1"
		}
	}
	port, err = strconv.Atoi(portStr)
	if err != nil || port < 0 || (!allowZero && port == 0) || port > 65535 {
		return "", 0, fmt.Errorf("invalid forward port %q (want [bind:]port)", spec)
	}
	if net.ParseIP(bind) == nil && bind != "localhost" {
		return "", 0, fmt.Errorf("invalid bind address %q", bind)
	}
	return bind, port, nil
}

// ForwardService starts an OpenShell-style local TCP forward to a sandbox
// loopback service. Port zero requests an OS-assigned local port.
func (a *App) ForwardService(sandbox, local, targetHost string, targetPort int) error {
	bind, localPort, err := parseForwardBind(local, true)
	if err != nil {
		return err
	}
	if targetPort <= 0 || targetPort > 65535 {
		return fmt.Errorf("invalid target port %d", targetPort)
	}
	if targetHost == "" {
		targetHost = "127.0.0.1"
	}
	if targetHost != "localhost" {
		ip := net.ParseIP(targetHost)
		if ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("target host %q is not loopback; only loopback services are supported", targetHost)
		}
	}
	resolved, err := a.resolveSandboxTarget(sandbox)
	if err != nil {
		return err
	}
	sandbox = resolved
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	client, err := a.gatewayForwardClient()
	if err != nil {
		return fmt.Errorf("forward service: %w", err)
	}
	defer client.Close()
	ln, err := net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(localPort)))
	if err != nil {
		return fmt.Errorf("forward service: listen: %w", err)
	}
	defer ln.Close()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return fmt.Errorf("forward service: unexpected listener address %T", ln.Addr())
	}
	id := sandbox + ":" + strconv.Itoa(addr.Port)
	path, err := forwardsPath()
	if err != nil {
		return err
	}
	_ = updateForward(path, id, map[string]any{
		"sandbox": sandbox, "bind": bind, "host_port": addr.Port, "guest_host": targetHost, "guest_port": targetPort, "pid": os.Getpid(),
	})
	defer func() { _ = updateForward(path, id, nil) }()
	target := net.JoinHostPort(targetHost, strconv.Itoa(targetPort))
	fmt.Printf("forward: %s → %s:%s via OpenShell ForwardTcp (Ctrl-C to stop)\n", ln.Addr(), sandbox, target)
	return acceptNativeForwards(ctx, ln, client, sandbox, targetHost, uint32(targetPort), id)
}

func forwardsPath() (string, error) {
	dir, err := localParityDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "forwards.json"), nil
}

// ForwardStart is OpenShell `forward start [bind:]port <sandbox> [-d]`:
// a local listener tunneled over the gateway SSH relay (direct-tcpip to the
// sandbox loopback). Foreground blocks until interrupted; background detaches.
func (a *App) ForwardStart(sandbox, spec, guestPort string, background bool) error {
	bind, port, err := ParseForwardBind(spec)
	if err != nil {
		return err
	}
	guest := port
	if g := strings.TrimSpace(guestPort); g != "" && g != spec {
		if guest, err = strconv.Atoi(g); err != nil || guest <= 0 || guest > 65535 {
			return fmt.Errorf("invalid guest port %q", guestPort)
		}
	}
	id := sandbox + ":" + strconv.Itoa(port)
	path, err := forwardsPath()
	if err != nil {
		return err
	}
	if background {
		return a.startForwardChild(id, sandbox, spec, path)
	}
	resolved, err := a.resolveSandboxTarget(sandbox)
	if err != nil {
		return err
	}
	sandbox = resolved
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	client, err := a.gatewayForwardClient()
	if err != nil {
		return fmt.Errorf("forward: %w", err)
	}
	defer client.Close()
	ln, err := net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("forward: listen: %w", err)
	}
	defer ln.Close()
	_ = updateForward(path, id, map[string]any{
		"sandbox": sandbox, "bind": bind, "host_port": port, "guest_port": guest, "pid": os.Getpid(),
	})
	defer func() { _ = updateForward(path, id, nil) }()
	fmt.Printf("forward: %s → %s:127.0.0.1:%d via OpenShell ForwardTcp (Ctrl-C to stop)\n", ln.Addr(), sandbox, guest)
	return acceptNativeForwards(ctx, ln, client, sandbox, "127.0.0.1", uint32(guest), "sandbox-forward:"+id)
}

func acceptNativeForwards(ctx context.Context, listener net.Listener, client *klorq.Client, sandbox, host string, port uint32, serviceID string) error {
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go relayLocalForward(ctx, client, sandbox, host, port, serviceID, conn)
	}
}

func (a *App) gatewayForwardClient() (*klorq.Client, error) {
	url, err := a.currentGatewayURL()
	if err != nil {
		return nil, err
	}
	if url == "" {
		return nil, fmt.Errorf("no Gateway selected; use cautem gateway select")
	}
	client := a.clientFor(url)
	client.Workspace = a.workspaceName()
	return client, nil
}

func relayLocalForward(ctx context.Context, client *klorq.Client, sandbox, host string, port uint32, serviceID string, local net.Conn) {
	defer local.Close()
	remote, err := client.OpenTCPForward(ctx, sandbox, serviceID, host, port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forward: %s:%d: %v\n", host, port, err)
		return
	}
	defer remote.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(remote, local); done <- struct{}{} }()
	go func() { _, _ = io.Copy(local, remote); done <- struct{}{} }()
	<-done
}

func (a *App) startForwardChild(id, sandbox, spec, registry string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logPath := strings.TrimSuffix(registry, ".json") + "-" + strings.NewReplacer(":", "-", "/", "-").Replace(id) + ".log"
	logF, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logF.Close()
	args := []string{}
	if a.GatewayURLOverride != "" {
		args = append(args, "-g", a.GatewayURLOverride)
	} else if a.GatewayNameOverride != "" {
		args = append(args, "-g", a.GatewayNameOverride)
	}
	args = append(args, "forward", "start", spec, sandbox)
	cmd := exec.Command(exe, args...)
	cmd.Stdout, cmd.Stderr = logF, logF
	cmd.SysProcAttr = gatewaySysProcAttr()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("forward: start background: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	time.Sleep(forwardStartupDelay)
	fmt.Printf("forward: running in background pid=%d (cautem forward stop %s; log %s)\n", cmd.Process.Pid, id, logPath)
	return nil
}

func updateForward(path, id string, rec map[string]any) error {
	m, err := readJSONMap(path)
	if err != nil {
		return err
	}
	if rec == nil {
		delete(m, id)
	} else {
		m[id] = rec
	}
	return writeJSONMap(path, m)
}

// ForwardStop stops a forward by id (<sandbox>:<port>) or port.
func (a *App) ForwardStop(id string) error {
	path, err := forwardsPath()
	if err != nil {
		return err
	}
	m, err := readJSONMap(path)
	if err != nil {
		return err
	}
	key := id
	if _, ok := m[key]; !ok {
		for k := range m {
			if strings.HasSuffix(k, ":"+id) {
				key = k
				break
			}
		}
	}
	rec, ok := m[key].(map[string]any)
	if !ok {
		return fmt.Errorf("forward %q not found (cautem forward list)", id)
	}
	if pid, ok := rec["pid"].(float64); ok && int(pid) > 0 && int(pid) != os.Getpid() {
		if p, err := os.FindProcess(int(pid)); err == nil {
			_ = p.Signal(syscall.SIGTERM)
		}
	}
	delete(m, key)
	if err := writeJSONMap(path, m); err != nil {
		return err
	}
	fmt.Printf("forward: stopped %s\n", key)
	return nil
}

// ForwardList prints active forwards.
func (a *App) ForwardList() error {
	path, err := forwardsPath()
	if err != nil {
		return err
	}
	m, err := readJSONMap(path)
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	fmt.Println(string(b))
	return nil
}
