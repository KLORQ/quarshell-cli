package service

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	datamodelv1 "github.com/KLORQ/quarshell-proto/gen/go/openshell/datamodel/v1"
	gatewayv1 "github.com/KLORQ/quarshell-proto/gen/go/openshell/gateway/v1"
	"google.golang.org/grpc"
)

type transferRouteGateway struct {
	gatewayv1.UnimplementedOpenShellServer
	name      string
	workspace string
}

func (g *transferRouteGateway) GetSandbox(_ context.Context, req *gatewayv1.GetSandboxRequest) (*gatewayv1.SandboxResponse, error) {
	g.name = req.GetName()
	g.workspace = req.GetWorkspaceScope().GetWorkspace()
	return &gatewayv1.SandboxResponse{Sandbox: &gatewayv1.Sandbox{
		Metadata: &datamodelv1.ObjectMeta{Name: req.GetName(), Workspace: req.GetWorkspaceScope().GetWorkspace()},
		Spec:     &gatewayv1.SandboxSpec{Template: &gatewayv1.SandboxTemplate{Image: "test"}},
	}}, nil
}

func TestCopyRoutesGatewaySandboxToGatewayBeforeLocalDriver(t *testing.T) {
	server, address, closeServer := startTransferRouteGateway(t)
	defer closeServer()
	app := &App{
		GatewayURLOverride: "http://" + address,
		GlobalWorkspace:    "team-a",
	}
	localDest := filepath.Join(t.TempDir(), "downloaded")
	err := app.Copy("shared:/workspace/report.txt", localDest)
	if err == nil || !strings.Contains(err.Error(), "Gateway download") {
		t.Fatalf("copy error=%v; want Gateway download exec route", err)
	}
	if server.name != "shared" || server.workspace != "team-a" {
		t.Fatalf("Gateway lookup targeted %q in workspace %q", server.name, server.workspace)
	}
}

func TestSandboxUploadRoutesGatewaySandboxBeforeLocalDriver(t *testing.T) {
	server, address, closeServer := startTransferRouteGateway(t)
	defer closeServer()
	app := &App{
		GatewayURLOverride: "http://" + address,
		GlobalWorkspace:    "team-a",
	}
	source := filepath.Join(t.TempDir(), "input.txt")
	if err := os.WriteFile(source, []byte("upload"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := app.SandboxUpload(source, "shared:/workspace", true)
	if err == nil {
		t.Fatal("upload unexpectedly succeeded without a Gateway exec service")
	}
	if server.name != "shared" || server.workspace != "team-a" {
		t.Fatalf("Gateway lookup targeted %q in workspace %q", server.name, server.workspace)
	}
}

func TestCopyRoutesOpenShellSandboxThroughGateway(t *testing.T) {
	_, address, closeServer := startTransferRouteGateway(t)
	defer closeServer()
	app := &App{
		GatewayURLOverride: "http://" + address,
		GlobalWorkspace:    "team-a",
	}
	if err := app.Copy("managed:/workspace/report.txt", filepath.Join(t.TempDir(), "report.txt")); err == nil || !strings.Contains(err.Error(), "Gateway download") {
		t.Fatalf("copy from OpenShell sandbox err=%v; want Gateway download route", err)
	}
}

func startTransferRouteGateway(t *testing.T) (*transferRouteGateway, string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &transferRouteGateway{}
	grpcServer := grpc.NewServer()
	gatewayv1.RegisterOpenShellServer(grpcServer, server)
	go func() { _ = grpcServer.Serve(listener) }()
	return server, listener.Addr().String(), func() {
		grpcServer.Stop()
		_ = listener.Close()
	}
}
