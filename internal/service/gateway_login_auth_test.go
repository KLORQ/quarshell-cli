package service

import (
	"testing"
	"time"

	"github.com/KLORQ/quarshell-cli/internal/storage/gwconfig"
	"github.com/KLORQ/quarshell-runtime/idp"
)

func TestGatewayLoginModesReplaceMutuallyExclusiveCredentials(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := gwconfig.File{Current: "remote", Gateways: map[string]gwconfig.Gateway{
		"remote": {URL: "https://gateway.example", Token: "old-bearer", EdgeToken: "old-edge", RefreshToken: "old-refresh", TokenExpiresAtMS: 123},
	}}
	if err := gwconfig.Save(cfg); err != nil {
		t.Fatal(err)
	}
	app := &App{}
	if err := app.storeOIDCBundle("remote", idp.TokenBundle{AccessToken: "oidc-access", RefreshToken: "oidc-refresh", Expiry: time.Unix(200, 0)}); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := gwconfig.Load()
	if err != nil {
		t.Fatal(err)
	}
	gateway := cfg.Gateways["remote"]
	if gateway.Token != "oidc-access" || gateway.RefreshToken != "oidc-refresh" || gateway.EdgeToken != "" {
		t.Fatalf("OIDC credentials did not replace edge credentials: %+v", gateway)
	}

	if err := app.GatewayLogin("explicit-bearer"); err != nil {
		t.Fatal(err)
	}
	cfg, _, err = gwconfig.Load()
	if err != nil {
		t.Fatal(err)
	}
	gateway = cfg.Gateways["remote"]
	if gateway.Token != "explicit-bearer" || gateway.RefreshToken != "" || gateway.TokenExpiresAtMS != 0 || gateway.EdgeToken != "" {
		t.Fatalf("explicit bearer login left another credential active: %+v", gateway)
	}

	gateway.EdgeToken = "edge-secret"
	gateway.Token = "stale-bearer"
	gateway.RefreshToken = "stale-refresh"
	gateway.TokenExpiresAtMS = 789
	cfg.Gateways["remote"] = gateway
	if err := gwconfig.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if err := app.GatewayLogout(); err != nil {
		t.Fatal(err)
	}
	cfg, _, err = gwconfig.Load()
	if err != nil {
		t.Fatal(err)
	}
	gateway = cfg.Gateways["remote"]
	if gateway.Token != "" || gateway.RefreshToken != "" || gateway.TokenExpiresAtMS != 0 || gateway.EdgeToken != "" {
		t.Fatalf("logout left credentials active: %+v", gateway)
	}
}
