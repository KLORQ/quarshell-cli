package service

import (
	"context"
	"crypto/tls"
	"fmt"
	"maps"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/KLORQ/quarshell-cli/internal/logger"
	"github.com/KLORQ/quarshell-cli/internal/storage/gwconfig"
	"github.com/KLORQ/quarshell-core/defaults"
	"github.com/KLORQ/quarshell-core/engine"
	"github.com/KLORQ/quarshell-core/env"
	"github.com/KLORQ/quarshell-core/policy"
	"github.com/KLORQ/quarshell-proxy/proxy"
	"github.com/KLORQ/quarshell-runtime/relayclient"
	"github.com/KLORQ/quarshell-runtime/relaytarget"
	"github.com/KLORQ/quarshell-runtime/supervisorcontrol"
	"github.com/KLORQ/quarshell-sdk/go/klorq"
	"github.com/KLORQ/quarshell-slogx"
)

// ProxyOpts for foreground host-side CONNECT proxy (debug / host-proxy mode).
type ProxyOpts struct {
	Listen          string
	Policy          string
	CAOut           string // write MITM CA PEM (for clients / sandbox trust)
	GatewayURL      string // optional: pull secrets + push OCSF
	GatewayHostMode bool   // GatewayURL already resolves from the proxy host; do not rewrite for a container sidecar
	Sandbox         string // sandbox name for gateway resolve/push
	LogDir          string // optional daily OCSF file dir (default /var/log)
}

// Proxy runs quarshell-proxy until interrupted.

// Proxy runs quarshell-proxy until interrupted.
func (a *App) Proxy(opt ProxyOpts) error {
	const op = "proxy.serve"
	ctx, cancel := a.withCancel()
	defer cancel()
	log := logger.Setup(ctx, logger.Options{Service: "quarshell-proxy"})
	ctx = logger.ToContext(ctx, log)
	log = log.With("op", op)

	// Read the supervisor token once and drop both credential sources from the
	// environment before the proxy snapshots os.Environ() into its secret store.
	sandboxToken, err := sandboxTokenFromEnvironment()
	if err != nil {
		return err
	}
	sandboxProtocolToken, err := sandboxProtocolTokenFromEnvironment()
	if err != nil {
		return err
	}
	_ = os.Unsetenv(EnvSandboxToken)
	_ = os.Unsetenv(EnvSandboxTokenFile)
	_ = os.Unsetenv(EnvSandboxProtocolToken)
	_ = os.Unsetenv(EnvSupervisorAuthBundleFile)

	listen := opt.Listen
	if listen == "" {
		listen = defaults.ProxyListenLocal()
	}
	doc, _, err := a.loadOrDenyAll(opt.Policy)
	if err != nil {
		return err
	}
	var eng engine.Allowlist
	if err := eng.Apply(doc); err != nil {
		return err
	}
	logDir := opt.LogDir
	if logDir == "" {
		logDir = os.Getenv("CAUTEM_LOG_DIR")
	}
	if logDir == "" {
		logDir = "/var/log"
	}
	gwURL := opt.GatewayURL
	if gwURL == "" {
		gwURL = os.Getenv("CAUTEM_GATEWAY_URL")
	}
	sandbox := opt.Sandbox
	if sandbox == "" {
		sandbox = strings.TrimSpace(os.Getenv("CAUTEM_GATEWAY_SANDBOX_ID"))
		if sandbox == "" {
			sandbox = os.Getenv("CAUTEM_SANDBOX")
		}
	}
	// OpenShell-style: CAUTEM_GATEWAY_URL must resolve via ExtraHosts
	// (host.cautem.internal → host-gateway). No hostname guessing.
	var gw *klorq.Client
	var pusher proxy.LogPusher
	if gwURL != "" && sandbox != "" {
		if !opt.GatewayHostMode {
			gwURL = GuestGatewayURL(gwURL)
		}
		gw = klorq.NewWithToken(gwURL, sandboxProtocolToken)
		gw.SetGRPCEndpoint(strings.TrimSpace(os.Getenv("CAUTEM_GATEWAY_GRPC_ENDPOINT")))
		pusher = gatewayAuditPusher{c: gw}
		if sandboxToken == "" {
			log.Warn("no sandbox supervisor token; gateway calls will be rejected", "env", EnvSandboxToken)
		}
	}
	audit := proxy.NewMultiAudit(os.Stderr, logDir, sandbox, pusher)
	defer func() {
		if err := audit.Close(); err != nil {
			log.Warn("proxy audit shutdown was incomplete", slogx.Err(err))
		}
	}()
	srv := proxy.NewServer(&eng, audit)
	srv.GatewayToken = sandboxProtocolToken
	if gw != nil && sandboxProtocolToken != "" {
		policyTLSConfig, tlsErr := relayclient.GatewayTLSConfigFromEnvironment()
		if tlsErr != nil {
			return tlsErr
		}
		srv.ReportPolicyStatus = func(reportCtx context.Context, revision uint32, loadError string) error {
			return relayclient.ReportPolicyStatusUntilAck(reportCtx, relayclient.Config{
				GatewayURL:          gwURL,
				GatewayGRPCEndpoint: strings.TrimSpace(os.Getenv("CAUTEM_GATEWAY_GRPC_ENDPOINT")),
				Sandbox:             sandbox,
				Token:               sandboxProtocolToken,
				TLSConfig:           policyTLSConfig,
			}, revision, loadError)
		}
	}

	// Prefer gateway-stored secrets; process environment remains a fallback only
	// for keys the gateway has never managed for this sandbox.
	if gw != nil {
		if err := a.refreshProxySecrets(srv, gw, sandbox); err != nil {
			log.Warn("gateway secrets unavailable", slogx.Err(err))
		} else {
			log.Info("secrets loaded from gateway", "sandbox", sandbox)
		}
		go func() {
			t := time.NewTicker(credentialRefreshInterval)
			defer t.Stop()
			for range t.C {
				_ = a.refreshProxySecrets(srv, gw, sandbox)
			}
		}()
	}

	// Supervisor relay (OpenShell ConnectSupervisor): outbound session to the
	// gateway. SSH may use a shared Unix socket, while lifecycle control can
	// independently use mTLS when the workload lives in another Pod.
	sshSocket := strings.TrimSpace(os.Getenv(EnvSSHSocket))
	supervisorControlSocket := strings.TrimSpace(os.Getenv("CAUTEM_SUPERVISOR_CONTROL_SOCKET"))
	controlAddress := supervisorControlListenAddress(
		os.Getenv(supervisorcontrol.EnvListen),
		os.Getenv(supervisorcontrol.EnvEndpoint),
	)
	controlRelay := controlAddress
	if controlRelay == "" {
		controlRelay = supervisorControlSocket
	}
	targetEndpoint := strings.TrimSpace(os.Getenv(relaytarget.EnvEndpoint))
	if supervisorRelayEnabled(sshSocket, controlRelay, targetEndpoint, gwURL, sandboxProtocolToken) {
		relayTLSConfig, err := relayclient.GatewayTLSConfigFromEnvironment()
		if err != nil {
			return err
		}
		var controlTLSConfig *tls.Config
		if controlAddress != "" || supervisorControlServerTLSConfigured() {
			controlTLSConfig, err = relayclient.SupervisorControlServerTLSConfigFromEnvironment()
			if err != nil {
				return err
			}
		}
		targetTLSConfig, err := relayclient.TargetTLSConfigFromEnvironment()
		if err != nil {
			return err
		}
		relayConfig := relayclient.Config{
			GatewayURL:                        gwURL,
			GatewayGRPCEndpoint:               strings.TrimSpace(os.Getenv("CAUTEM_GATEWAY_GRPC_ENDPOINT")),
			GatewayToken:                      sandboxToken,
			SupervisorControlSocket:           supervisorControlSocket,
			SupervisorControlAddress:          controlAddress,
			SupervisorControlTLSConfig:        controlTLSConfig,
			SupervisorControlExpectedIdentity: strings.TrimSpace(os.Getenv(supervisorcontrol.EnvExpectedClientIdentity)),
			IsolationDescriptorPath:           strings.TrimSpace(os.Getenv("CAUTEM_ISOLATION_DESCRIPTOR_PATH")),
			IsolationSandboxID:                strings.TrimSpace(os.Getenv("CAUTEM_SANDBOX")),
			AdmittedIsolationBackend:          strings.TrimSpace(os.Getenv("CAUTEM_ADMITTED_ISOLATION_BACKEND")),
			Sandbox:                           sandbox,
			Token:                             sandboxProtocolToken,
			SSHSocket:                         sshSocket,
			TargetDialSocket:                  strings.TrimSpace(os.Getenv(EnvTargetDialSocket)),
			TargetDialEndpoint:                targetEndpoint,
			TargetTLSConfig:                   targetTLSConfig,
			TLSConfig:                         relayTLSConfig,
			Log:                               log.Logger,
			OnCredentialsRefreshed: func(gatewayToken, refreshedSandboxToken string) error {
				if strings.TrimSpace(gatewayToken) == "" {
					return fmt.Errorf("gateway returned an empty refreshed gateway bearer")
				}
				if strings.TrimSpace(refreshedSandboxToken) == "" {
					return fmt.Errorf("gateway returned an empty refreshed sandbox bearer")
				}
				if gw != nil {
					gw.SetToken(refreshedSandboxToken)
				}
				srv.SetGatewayToken(refreshedSandboxToken)
				return nil
			},
		}
		if err := relayConfig.ValidateTargetTransport(); err != nil {
			return err
		}
		go func() {
			if err := relayclient.Run(ctx, relayConfig); err != nil && ctx.Err() == nil {
				log.Error("supervisor relay stopped", slogx.Err(err))
			}
		}()
		log.Info("supervisor relay enabled", "ssh_socket", sshSocket, "control_mtls", controlTLSConfig != nil)
	}

	if ca := srv.CA(); ca != nil && opt.CAOut != "" {
		if err := ca.WriteBundle(opt.CAOut); err != nil {
			return fmt.Errorf("proxy ca-out: %w", err)
		}
		log.Info("wrote mitm ca bundle", "path", opt.CAOut)
	}
	proxy.LifecycleReady(audit, "proxy ready on "+listen)
	log.Info("listening", "addr", listen, "allow_rules", len(doc.AllowRules()), "mitm_ca", srv.CA() != nil)
	if path := strings.TrimSpace(opt.Policy); path != "" {
		go srv.WatchPolicy(ctx, path, time.Second)
		log.Info("watching policy for hot-reload", "path", path)
	}
	return serveProxy(ctx, srv, listen)
}

func supervisorControlServerTLSConfigured() bool {
	return strings.TrimSpace(os.Getenv(supervisorcontrol.EnvServerCA)) != "" ||
		strings.TrimSpace(os.Getenv(supervisorcontrol.EnvServerCert)) != "" ||
		strings.TrimSpace(os.Getenv(supervisorcontrol.EnvServerKey)) != ""
}

func serveProxy(ctx context.Context, server *proxy.Server, address string) error {
	if strings.HasPrefix(address, "unix://") {
		return serveUnixProxy(ctx, server, strings.TrimPrefix(address, "unix://"))
	}
	return server.ListenAndServe(ctx, address)
}

func supervisorRelayEnabled(sshSocket, controlAddress, targetEndpoint, gatewayURL, token string) bool {
	return (strings.TrimSpace(sshSocket) != "" || strings.TrimSpace(controlAddress) != "" || strings.TrimSpace(targetEndpoint) != "") &&
		strings.TrimSpace(gatewayURL) != "" && strings.TrimSpace(token) != ""
}

func (a *App) refreshProxySecrets(srv *proxy.Server, c *klorq.Client, sandbox string) error {
	ctx, cancel := context.WithTimeout(context.Background(), TimeoutAPIShort)
	defer cancel()
	m, err := c.ResolveProxySecrets(ctx, sandbox)
	if err != nil {
		return err
	}
	a.secretMu.Lock()
	if a.gatewaySecretKeys == nil {
		a.gatewaySecretKeys = make(map[string]managedSecretKeys)
	}
	managed := a.gatewaySecretKeys[sandbox]
	if managed == nil {
		managed = make(managedSecretKeys)
		a.gatewaySecretKeys[sandbox] = managed
	}
	for key := range m {
		managed[key] = struct{}{}
	}
	if m["GITHUB_TOKEN"] != "" {
		managed["GH_TOKEN"] = struct{}{}
	}
	if m["GH_TOKEN"] != "" {
		managed["GITHUB_TOKEN"] = struct{}{}
	}
	store := gatewayProxySecretSnapshot(proxy.LoadSecretsFromEnviron(os.Environ()), m, managed)
	a.secretMu.Unlock()
	srv.SetSecrets(store)
	return nil
}

func gatewayProxySecretSnapshot(environment, gateway map[string]string, managed managedSecretKeys) proxy.SecretStore {
	store := proxy.SecretStore{}
	for key, value := range environment {
		if _, wasGatewayManaged := managed[key]; !wasGatewayManaged {
			store[key] = value
		}
	}
	maps.Copy(store, gateway)
	// Mirror GitHub token aliases (policy/credential_keys may list both).
	if v := store["GITHUB_TOKEN"]; v != "" {
		if _, ok := store["GH_TOKEN"]; !ok {
			store["GH_TOKEN"] = v
		}
	}
	if v := store["GH_TOKEN"]; v != "" {
		if _, ok := store["GITHUB_TOKEN"]; !ok {
			store["GITHUB_TOKEN"] = v
		}
	}
	return store
}

// GuestGatewayURL rewrites loopback (and legacy host.docker.internal) gateway URLs
// to host.cautem.internal — the OpenShell-style host-gateway alias injected via ExtraHosts.

// GuestGatewayURL rewrites loopback (and legacy host.docker.internal) gateway URLs
// to host.cautem.internal — the OpenShell-style host-gateway alias injected via ExtraHosts.
func GuestGatewayURL(gwURL string) string {
	raw := strings.TrimSpace(gwURL)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	switch strings.ToLower(u.Hostname()) {
	case "127.0.0.1", "localhost", "::1", "host.docker.internal":
		port := u.Port()
		u.Host = defaults.HostInternal
		if port != "" {
			u.Host = net.JoinHostPort(defaults.HostInternal, port)
		}
	}
	return u.String()
}

// gatewayAuditPusher adapts SDK client to proxy.LogPusher.

// gatewayAuditPusher adapts SDK client to proxy.LogPusher.
type gatewayAuditPusher struct {
	c *klorq.Client
}

func (g gatewayAuditPusher) PostLogs(ctx context.Context, sandbox string, lines []proxy.AuditLine) error {
	if g.c == nil || len(lines) == 0 {
		return nil
	}
	out := make([]klorq.LogLine, len(lines))
	for i, l := range lines {
		out[i] = klorq.LogLine{TS: l.TS, Source: l.Source, Level: l.Level, Text: l.Text}
	}
	return g.c.PostSandboxLogs(ctx, sandbox, out)
}

func hostEnvForPolicy(doc policy.Document) []string {
	out := env.FromHostForGuest(doc.CredentialEnvKeys()...)
	// Keep guest agent installs on PATH even when login shells reset it.
	out = append(out,
		"HOME="+defaults.GuestHome,
		"PATH="+defaults.GuestPath,
	)
	return out
}

// credentialPlaceholdersForSandbox returns cautem:resolve:env placeholders for the
// sandbox effective policy credential keys (and attached provider guest keys).
// Profiles with inject_env: false (Cursor) are skipped — Agent validates the key
// client-side and rejects placeholders.

// credentialPlaceholdersForSandbox returns cautem:resolve:env placeholders for the
// sandbox effective policy credential keys (and attached provider guest keys).
// Profiles with inject_env: false (Cursor) are skipped — Agent validates the key
// client-side and rejects placeholders.
func (a *App) credentialPlaceholdersForSandbox(sandbox string) []string {
	keys := a.sandboxCredentialKeys(sandbox)
	return env.FromHostForGuest(keys...)
}

func (a *App) sandboxCredentialKeys(sandbox string) []string {
	var keys []string
	seen := map[string]struct{}{}
	omit := map[string]struct{}{} // inject_env:false — never guest-inject
	add := func(list []string) {
		for _, k := range list {
			k = strings.TrimSpace(k)
			if k == "" {
				continue
			}
			if _, skip := omit[k]; skip {
				continue
			}
			if _, ok := seen[k]; ok {
				continue
			}
			seen[k] = struct{}{}
			keys = append(keys, k)
		}
	}
	if c, err := a.gatewayClient(); err == nil {
		ctx, cancel := a.withTimeout(TimeoutAPIShort)
		defer cancel()
		if sb, err := c.GetControlSandbox(ctx, a.GlobalWorkspace, sandbox); err == nil {
			for _, name := range sb.AttachedProviders {
				rec, err := c.GetProvider(ctx, name)
				if err != nil {
					continue
				}
				prof, err := loadBuiltinProfile(rec.Type)
				if err != nil {
					// Unknown profile: fall back to instance env vars.
					add(rec.EnvVars)
					continue
				}
				for _, cred := range prof.Credentials {
					if cred.InjectEnv != nil && !*cred.InjectEnv {
						for _, k := range cred.EnvVars {
							omit[strings.TrimSpace(k)] = struct{}{}
						}
					}
				}
				guest := prof.GuestEnvKeys()
				if len(rec.EnvVars) > 0 {
					// Restrict to instance keys, still honoring inject_env:false.
					var filtered []string
					for _, k := range rec.EnvVars {
						if _, skip := omit[k]; skip {
							continue
						}
						filtered = append(filtered, k)
					}
					guest = filtered
				}
				add(guest)
			}
		}
		if b, err := c.EffectivePolicy(ctx, sandbox); err == nil {
			if doc, err := policy.Parse(b); err == nil {
				add(doc.CredentialEnvKeys())
			}
		}
	}
	return keys
}

func mergeEnvEntries(base, extra []string) []string {
	keys := map[string]int{}
	out := make([]string, 0, len(base)+len(extra))
	add := func(entry string) {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			return
		}
		if i, exists := keys[key]; exists {
			out[i] = entry
			return
		}
		keys[key] = len(out)
		out = append(out, entry)
	}
	for _, e := range base {
		add(e)
	}
	for _, e := range extra {
		add(e)
	}
	return out
}

func proxySecretsForPolicy(doc policy.Document) []string {
	out := env.SecretsFromHost(doc.CredentialEnvKeys()...)
	for _, k := range defaults.ProxyEnvKeys {
		if v, ok := os.LookupEnv(k); ok && v != "" {
			out = append(out, k+"="+v)
		}
	}
	return out
}

// EnvSandboxToken carries the sandbox-scoped supervisor token into the proxy
// sidecar only (never the sandbox container).

// EnvSandboxToken carries the sandbox-scoped supervisor token into the proxy
// sidecar only (never the sandbox container).
const EnvSandboxToken = "CAUTEM_SANDBOX_TOKEN"

// EnvSandboxProtocolToken carries the distinct Sandbox Protocol bearer from a
// paired Gateway/Sandbox runtime credential set.
const EnvSandboxProtocolToken = "CAUTEM_SANDBOX_PROTOCOL_TOKEN"

// EnvSandboxTokenFile points to a file containing the sandbox supervisor
// bearer. Kubernetes uses this form so the token never appears in Pod env.
const EnvSandboxTokenFile = "CAUTEM_SANDBOX_TOKEN_FILE"

// EnvSupervisorAuthBundleFile points to the driver's private, descriptor-bound
// host supervisor authentication bundle.
const EnvSupervisorAuthBundleFile = "CAUTEM_SUPERVISOR_AUTH_BUNDLE_FILE"

func supervisorControlListenAddress(listen, legacyEndpoint string) string {
	if address := strings.TrimSpace(listen); address != "" {
		return address
	}
	return strings.TrimSpace(legacyEndpoint)
}

// EnvSSHSocket is set by the driver when the sidecar shares the sshd socket.

// EnvSSHSocket is set by the driver when the sidecar shares the sshd socket.
const EnvSSHSocket = "CAUTEM_SSH_SOCKET"

const EnvTargetDialSocket = "CAUTEM_TCP_DIAL_SOCKET"

// proxyGatewayEnv adds CAUTEM_GATEWAY_URL / CAUTEM_SANDBOX /
// CAUTEM_SANDBOX_TOKEN so the sidecar can resolve encrypted provider
// secrets, push OCSF logs and run the supervisor relay (OpenShell-like).

// proxyGatewayEnv adds CAUTEM_GATEWAY_URL / CAUTEM_SANDBOX /
// CAUTEM_SANDBOX_TOKEN so the sidecar can resolve encrypted provider
// secrets, push OCSF logs and run the supervisor relay (OpenShell-like).
func (a *App) proxyGatewayEnv(sandbox, gwURL, sandboxToken string) []string {
	var out []string
	if gwURL == "" {
		if cfg, _, err := gwconfig.Load(); err == nil {
			gwURL = gwconfig.CurrentURL(cfg)
		}
	}
	if gwURL == "" {
		return out
	}
	guestGW := GuestGatewayURL(gwURL)
	out = append(out, "CAUTEM_GATEWAY_URL="+guestGW)
	if sandbox != "" {
		out = append(out, "CAUTEM_SANDBOX="+sandbox)
	}
	if sandboxToken != "" {
		out = append(out, EnvSandboxToken+"="+sandboxToken)
	}
	out = append(out, "CAUTEM_LOG_DIR=/var/log")
	return out
}
