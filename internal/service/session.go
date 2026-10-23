package service

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/KLORQ/quarshell-cli/internal/global"
	"github.com/KLORQ/quarshell-cli/internal/storage/gwconfig"
	"github.com/KLORQ/quarshell-core/defaults"
	"github.com/KLORQ/quarshell-runtime/idp"
	"github.com/KLORQ/quarshell-sdk/go/klorq"
)

var errNoCurrentGateway = errors.New("no current gateway; run: quarshell gateway add|select")

// ApplyGlobal stores OpenShell-style global flags for this CLI session.
func (a *App) ApplyGlobal(g global.Context) {
	if g.Output != "" {
		a.OutputFormat = g.Output
	}
	if g.Workspace != "" {
		a.GlobalWorkspace = g.Workspace
	}
	a.GatewayURLOverride = strings.TrimSpace(g.GatewayURL)
	a.GatewayNameOverride = strings.TrimSpace(g.GatewayName)
	a.GatewayInsecure = g.GatewayInsecure
}

func looksLikeGatewayURL(s string) bool {
	s = strings.TrimSpace(s)
	return strings.Contains(s, "://") || strings.HasPrefix(s, "127.") || strings.HasPrefix(s, "localhost")
}

// currentGatewayURL resolves the active gateway endpoint.
// Order: App URL override, OPENSHELL_GATEWAY/QUARSHELL_GATEWAY_URL, -g name, config current.
func (a *App) currentGatewayURL() (string, error) {
	if a != nil && a.GatewayURLOverride != "" {
		return strings.TrimRight(a.GatewayURLOverride, "/"), nil
	}
	if v := firstNonEmptyEnv("OPENSHELL_GATEWAY", "QUARSHELL_GATEWAY_URL"); v != "" {
		if looksLikeGatewayURL(v) {
			return strings.TrimRight(v, "/"), nil
		}
		cfg, _, err := gwconfig.Load()
		if err != nil {
			return "", err
		}
		if g, ok := cfg.Gateways[v]; ok && g.URL != "" {
			return strings.TrimRight(g.URL, "/"), nil
		}
		return "", fmt.Errorf("gateway %q from env not in config (quarshell gateway add)", v)
	}
	if a != nil && a.GatewayNameOverride != "" {
		cfg, _, err := gwconfig.Load()
		if err != nil {
			return "", err
		}
		g, ok := cfg.Gateways[a.GatewayNameOverride]
		if !ok || g.URL == "" {
			return "", fmt.Errorf("gateway %q not in config", a.GatewayNameOverride)
		}
		return strings.TrimRight(g.URL, "/"), nil
	}
	cfg, _, err := gwconfig.Load()
	if err != nil {
		return "", err
	}
	u := gwconfig.CurrentURL(cfg)
	if u == "" {
		return "", errNoCurrentGateway
	}
	return strings.TrimRight(u, "/"), nil
}

// GatewayTUIConfig returns authenticated gateway parameters for the TUI.
// A false ok lets the CLI use its local runtime when no gateway is selected.
func (a *App) GatewayTUIConfig() (url, workspace, token string, ok bool, err error) {
	url, err = a.currentGatewayURL()
	if errors.Is(err, errNoCurrentGateway) {
		return "", "", "", false, nil
	}
	if err != nil {
		return "", "", "", false, err
	}
	return url, a.workspaceName(), a.gatewayTokenForURL(url), true, nil
}

// gatewayTokenForURL resolves the bearer for a gateway URL:
// $QUARSHELL_GATEWAY_TOKEN, then the config token, then the gateway's
// owner-only <data_dir>/auth_token (local gateways started by this CLI).
func (a *App) gatewayTokenForURL(url string) string {
	if v := strings.TrimSpace(os.Getenv(klorq.EnvToken)); v != "" {
		return v
	}
	url = strings.TrimRight(url, "/")
	dataDir := ""
	if a != nil {
		a.tokenMu.Lock()
		defer a.tokenMu.Unlock()
	}
	if cfg, _, err := gwconfig.Load(); err == nil {
		for name, g := range cfg.Gateways {
			if strings.TrimRight(g.URL, "/") != url {
				continue
			}
			if token, refreshed, refreshErr := a.refreshGatewayToken(cfg, name, g); refreshed {
				if refreshErr != nil {
					slog.Warn("gateway OIDC token refresh failed; run quarshell gateway login", "gateway", name)
					return g.Token
				}
				return token
			}
			if tok := strings.TrimSpace(g.Token); tok != "" {
				return tok
			}
			if g.DataDir != "" {
				dataDir = g.DataDir
			}
		}
	}
	if dataDir == "" && isLocalGatewayURL(url) {
		dataDir = defaultGatewayDataDir()
	}
	if dataDir == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(dataDir, gatewayAuthTokenFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

const gatewayTokenRefreshSkew = time.Minute

// refreshGatewayToken refreshes only configured OIDC sessions and persists any
// rotated refresh token before returning the new access token. The caller holds
// tokenMu so concurrent requests in this CLI process share one refresh.
func (a *App) refreshGatewayToken(cfg gwconfig.File, name string, gateway gwconfig.Gateway) (string, bool, error) {
	if gateway.TokenExpiresAtMS == 0 || gateway.RefreshToken == "" || gateway.OIDCIssuer == "" || gateway.OIDCClientID == "" {
		return "", false, nil
	}
	expiresAt := time.UnixMilli(gateway.TokenExpiresAtMS)
	if time.Until(expiresAt) > gatewayTokenRefreshSkew {
		return "", false, nil
	}
	if a == nil {
		return "", true, errors.New("gateway app is unavailable")
	}
	ctx, cancel := a.withTimeout(TimeoutAPI)
	defer cancel()
	bundle, err := idp.RefreshTokens(ctx, idp.PKCEConfig{
		Issuer:            gateway.OIDCIssuer,
		ClientID:          gateway.OIDCClientID,
		Audience:          gateway.OIDCAudience,
		Scopes:            gateway.OIDCScopes,
		AllowInsecureHTTP: gateway.OIDCAllowHTTP,
	}, gateway.RefreshToken)
	if err != nil {
		return "", true, err
	}
	gateway.Token = bundle.AccessToken
	if bundle.RefreshToken != "" {
		gateway.RefreshToken = bundle.RefreshToken
	}
	if !bundle.Expiry.IsZero() {
		gateway.TokenExpiresAtMS = bundle.Expiry.UnixMilli()
	} else {
		// Do not keep an already expired timestamp and trigger a refresh on
		// every subsequent API call when the IdP omits expires_in.
		gateway.TokenExpiresAtMS = 0
	}
	cfg.Gateways[name] = gateway
	if err := gwconfig.Save(cfg); err != nil {
		// The returned access token is valid for this invocation even if the
		// refreshed bundle could not be written back for the next invocation.
		slog.Warn("refreshed gateway OIDC token could not be saved", "gateway", name)
	}
	return gateway.Token, true, nil
}

// gatewayAuthTokenFile mirrors the gateway store.AuthTokenFile.
const gatewayAuthTokenFile = "auth_token"

func isLocalGatewayURL(u string) bool {
	u = strings.TrimRight(u, "/")
	return u == localGatewayURL ||
		u == "https://localhost:17670" ||
		u == "http://127.0.0.1:"+strconv.Itoa(defaults.GatewayPort) ||
		u == "http://localhost:"+strconv.Itoa(defaults.GatewayPort)
}

// defaultGatewayDataDir mirrors quarshell-gateway's default --data-dir.
func defaultGatewayDataDir() string {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "quarshell", "gateway")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "quarshell-gateway")
	}
	return filepath.Join(home, ".local", "state", "quarshell", "gateway")
}

// clientFor returns an authenticated client for a gateway URL.
func (a *App) clientFor(u string) *klorq.Client {
	client := a.clientWithToken(u, a.gatewayTokenForURL(u))
	client.Workspace = a.GlobalWorkspace
	if isLocalGatewayURL(u) {
		if err := configureLocalTLS(client); err != nil {
			client.SetInsecureSkipVerify(true)
		}
		client.SetGRPCEndpoint(localGatewayGRPCEndpoint)
	}
	return client
}

func configureLocalTLS(client *klorq.Client) error {
	dir := filepath.Join(defaultGatewayDataDir(), "tls")
	return client.SetTLSCertificate(
		filepath.Join(dir, "client", "client.crt"),
		filepath.Join(dir, "client", "client.key"),
		filepath.Join(dir, "ca.crt"),
	)
}

func (a *App) clientWithToken(u, token string) *klorq.Client {
	var edgeToken string
	if tunneledURL, tokenFromEdge, ok := a.edgeTunnelURL(u); ok {
		u = tunneledURL
		token = ""
		edgeToken = tokenFromEdge
	}
	client := klorq.NewWithToken(u, token)
	client.SetEdgeToken(edgeToken)
	if isLocalGatewayURL(u) {
		if err := configureLocalTLS(client); err != nil {
			client.SetInsecureSkipVerify(true)
		}
		client.SetGRPCEndpoint(localGatewayGRPCEndpoint)
	}
	if a != nil && a.GatewayInsecure {
		client.SetInsecureSkipVerify(true)
	}
	return client
}

func firstNonEmptyEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func (a *App) gatewayClient() (*klorq.Client, error) {
	if err := a.GatewayEnsure(); err != nil {
		return nil, err
	}
	u, err := a.currentGatewayURL()
	if err != nil {
		return nil, err
	}
	if u == "" {
		return nil, fmt.Errorf("no gateway selected (quarshell gateway ensure|add|select)")
	}
	return a.clientFor(u), nil
}
