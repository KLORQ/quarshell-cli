package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/KLORQ/quarshell-cli/internal/storage/gwconfig"
	display "github.com/KLORQ/quarshell-display"
	"github.com/KLORQ/quarshell-runtime/idp"
)

// GatewayLoginInteractive stores a bearer token.
// OIDC gateways use Authorization Code + PKCE. Local gateways read the
// token written to their configured data directory, matching OpenShell CLI.
func (a *App) GatewayLoginInteractive(token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		token = strings.TrimSpace(os.Getenv("CAUTEM_GATEWAY_TOKEN"))
	}
	if token != "" {
		return a.GatewayLogin(token)
	}
	cfg, _, err := gwconfig.Load()
	if err != nil {
		return err
	}
	if cfg.Current == "" {
		return fmt.Errorf("no gateway selected")
	}
	g := cfg.Gateways[cfg.Current]

	issuer := strings.TrimSpace(g.OIDCIssuer)
	clientID := strings.TrimSpace(g.OIDCClientID)
	audience := strings.TrimSpace(g.OIDCAudience)
	scopes := strings.TrimSpace(g.OIDCScopes)
	allowHTTP := g.OIDCAllowHTTP
	if issuer == "" {
		if meta, err := fetchOIDCMeta(g.URL); err == nil {
			issuer = meta.Issuer
			if audience == "" {
				audience = meta.Audience
			}
		}
	}
	if issuer != "" {
		if clientID == "" {
			clientID = firstNonEmptyEnv("CAUTEM_OIDC_CLIENT_ID", "OPENSHELL_OIDC_CLIENT_ID")
		}
		if clientID == "" {
			clientID = "openshell-cli"
		}
		ctx, cancel := a.withTimeout(TimeoutWaitLong)
		defer cancel()
		bundle, err := idp.BrowserPKCE(ctx, idp.PKCEConfig{
			Issuer:            issuer,
			ClientID:          clientID,
			Audience:          audience,
			Scopes:            scopes,
			AllowInsecureHTTP: allowHTTP,
			OpenURL:           openBrowser,
			RedirectPort:      18765, // fixed for Dex/Keycloak static redirect allowlists
		})
		if err != nil {
			return fmt.Errorf("gateway login oidc: %w", err)
		}
		return a.storeOIDCBundle(cfg.Current, bundle)
	}
	tok := a.gatewayTokenForURL(g.URL)
	if tok != "" {
		return a.GatewayLogin(tok)
	}
	if isLocalGatewayURL(g.URL) {
		return fmt.Errorf("gateway login: no local auth token found; pass --token or configure the gateway's data_dir with its auth_token file")
	}
	ctx, cancel := a.withTimeout(TimeoutWaitLong)
	defer cancel()
	tok, err = browserEdgeAuth(ctx, g.URL)
	if err != nil {
		return fmt.Errorf("gateway login edge auth: %w", err)
	}
	g.EdgeToken = tok
	g.Token = ""
	g.RefreshToken = ""
	g.TokenExpiresAtMS = 0
	cfg.Gateways[cfg.Current] = g
	if err := gwconfig.Save(cfg); err != nil {
		return err
	}
	fmt.Printf("logged in to gateway %s via edge authentication\n", cfg.Current)
	return nil
}

func (a *App) storeOIDCBundle(name string, bundle idp.TokenBundle) error {
	cfg, _, err := gwconfig.Load()
	if err != nil {
		return err
	}
	g := cfg.Gateways[name]
	g.Token = bundle.AccessToken
	g.EdgeToken = ""
	g.RefreshToken = bundle.RefreshToken
	if !bundle.Expiry.IsZero() {
		g.TokenExpiresAtMS = bundle.Expiry.UnixMilli()
	} else {
		g.TokenExpiresAtMS = 0
	}
	cfg.Gateways[name] = g
	if err := gwconfig.Save(cfg); err != nil {
		return err
	}
	fmt.Printf("logged in to gateway %s via OIDC\n", name)
	return nil
}

type oidcMeta struct {
	Issuer   string `json:"issuer"`
	Audience string `json:"audience"`
}

func fetchOIDCMeta(gatewayURL string) (oidcMeta, error) {
	ctx, cancel := context.WithTimeout(context.Background(), TimeoutAPIShort)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(gatewayURL, "/")+"/auth/oidc-config", nil)
	if err != nil {
		return oidcMeta{}, err
	}
	res, err := (&http.Client{Timeout: TimeoutAPI}).Do(req)
	if err != nil {
		return oidcMeta{}, err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return oidcMeta{}, fmt.Errorf("%s", res.Status)
	}
	var m oidcMeta
	if err := json.NewDecoder(res.Body).Decode(&m); err != nil {
		return oidcMeta{}, err
	}
	return m, nil
}

func openBrowser(url string) error {
	return display.OpenHostBrowser(url)
}
