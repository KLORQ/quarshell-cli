package service

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/KLORQ/quarshell-cli/internal/storage/gwconfig"
	gatewayv1 "github.com/KLORQ/quarshell-proto/gen/go/openshell/gateway/v1"
	"github.com/coder/websocket"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestEdgeTunnelProxyCarriesRawTCPOverWebSocket(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_ws_tunnel" {
			t.Errorf("unexpected tunnel path %q", r.URL.Path)
		}
		if r.Header.Get("Cookie") != "CF_Authorization=edge-secret" || r.Header.Get("Cf-Access-Token") != "edge-secret" {
			t.Errorf("edge headers missing: %v", r.Header)
		}
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		for {
			typ, data, err := ws.Read(r.Context())
			if err != nil {
				return
			}
			if err := ws.Write(r.Context(), typ, data); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	addr, err := startEdgeTunnelProxy(server.URL, "edge-secret")
	if err != nil {
		t.Fatal(err)
	}
	tcp, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	_ = tcp.SetDeadline(time.Now().Add(3 * time.Second))
	payload := []byte("native grpc bytes")
	if _, err := tcp.Write(payload); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(tcp, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("echo = %q, want %q", got, payload)
	}
}

func TestNewEdgeAuthCodeMatchesOpenShellShape(t *testing.T) {
	code, err := newEdgeAuthCode()
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != 8 || code[3] != '-' {
		t.Fatalf("code %q does not use XXX-XXXX format", code)
	}
	for i, char := range code {
		if i == 3 {
			continue
		}
		if !strings.ContainsRune(edgeAuthCodeAlphabet, char) {
			t.Fatalf("unexpected code character %q", char)
		}
	}
}

func TestClientUsesEdgeTunnelForNativeGatewayRPC(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	gateway := newEdgeTunnelTestGateway()
	grpcListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	grpcServer := grpc.NewServer()
	gatewayv1.RegisterOpenShellServer(grpcServer, gateway)
	go func() { _ = grpcServer.Serve(grpcListener) }()
	t.Cleanup(func() {
		grpcServer.Stop()
		_ = grpcListener.Close()
	})

	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_ws_tunnel" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Cf-Access-Token") != "edge-secret" || r.Header.Get("Cf-Access-Jwt-Assertion") != "edge-secret" || r.Header.Get("Cookie") != "CF_Authorization=edge-secret" {
			http.Error(w, "missing edge credentials", http.StatusForbidden)
			return
		}
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		tcp, err := net.Dial("tcp", grpcListener.Addr().String())
		if err != nil {
			return
		}
		defer tcp.Close()
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		done := make(chan struct{}, 2)
		go func() {
			defer func() { done <- struct{}{} }()
			for {
				_, data, readErr := ws.Read(ctx)
				if readErr != nil {
					return
				}
				if _, writeErr := tcp.Write(data); writeErr != nil {
					return
				}
			}
		}()
		go func() {
			defer func() { done <- struct{}{} }()
			buffer := make([]byte, 32*1024)
			for {
				n, readErr := tcp.Read(buffer)
				if n > 0 {
					if writeErr := ws.Write(ctx, websocket.MessageBinary, buffer[:n]); writeErr != nil {
						return
					}
				}
				if readErr != nil {
					return
				}
			}
		}()
		<-done
	}))
	defer front.Close()
	if err := gwconfig.Save(gwconfig.File{Current: "remote", Gateways: map[string]gwconfig.Gateway{
		"remote": {URL: front.URL, EdgeToken: "edge-secret"},
	}}); err != nil {
		t.Fatal(err)
	}

	client := (&App{}).clientWithToken(front.URL, "stale-bearer")
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	items, _, err := client.ListSandboxesPage(ctx, 10, "", "", false)
	if err != nil {
		t.Fatalf("ListSandboxesPage over edge tunnel: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("sandbox list = %+v, want empty response", items)
	}
	select {
	case auth := <-gateway.authMetadata:
		if got := auth.Get("cf-access-jwt-assertion"); len(got) != 1 || got[0] != "edge-secret" {
			t.Fatalf("edge JWT metadata = %v", got)
		}
		if got := auth.Get("cookie"); len(got) != 1 || got[0] != "CF_Authorization=edge-secret" {
			t.Fatalf("edge cookie metadata = %v", got)
		}
		if got := auth.Get("authorization"); len(got) != 0 {
			t.Fatalf("stale bearer metadata must not override edge auth: %v", got)
		}
	case <-ctx.Done():
		t.Fatal("gateway did not receive OpenShell edge auth metadata")
	}
}

type edgeTunnelTestGateway struct {
	gatewayv1.UnimplementedOpenShellServer
	authMetadata chan metadata.MD
}

func newEdgeTunnelTestGateway() *edgeTunnelTestGateway {
	return &edgeTunnelTestGateway{authMetadata: make(chan metadata.MD, 1)}
}

func (s *edgeTunnelTestGateway) ListSandboxes(ctx context.Context, _ *gatewayv1.ListSandboxesRequest) (*gatewayv1.ListSandboxesResponse, error) {
	if auth, ok := metadata.FromIncomingContext(ctx); ok {
		s.authMetadata <- auth
	}
	return &gatewayv1.ListSandboxesResponse{}, nil
}
