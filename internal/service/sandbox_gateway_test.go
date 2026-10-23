package service

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KLORQ/quarshell-cli/internal/storage/templates"
	runtimev1 "github.com/KLORQ/quarshell-proto/gen/go/cautem/runtime/v1"
	datamodelv1 "github.com/KLORQ/quarshell-proto/gen/go/openshell/datamodel/v1"
	gatewayv1 "github.com/KLORQ/quarshell-proto/gen/go/openshell/gateway/v1"
	"google.golang.org/grpc"
)

type remoteLookupTestServer struct {
	gatewayv1.UnimplementedOpenShellServer
	runtimev1.UnimplementedMainProcessServiceServer
	commands        [][]string
	mainAttachStart *runtimev1.MainProcessTTYStart
}

func (s *remoteLookupTestServer) ExecSandboxInteractive(stream grpc.BidiStreamingServer[gatewayv1.ExecSandboxInput, gatewayv1.ExecSandboxEvent]) error {
	start, err := stream.Recv()
	if err != nil {
		return err
	}
	request := start.GetStart()
	s.commands = append(s.commands, append([]string(nil), request.GetCommand()...))
	if err := stream.Send(&gatewayv1.ExecSandboxEvent{Payload: &gatewayv1.ExecSandboxEvent_Stdout{Stdout: &gatewayv1.ExecSandboxStdout{Data: []byte("foreground-output\n")}}}); err != nil {
		return err
	}
	return stream.Send(&gatewayv1.ExecSandboxEvent{Payload: &gatewayv1.ExecSandboxEvent_Exit{Exit: &gatewayv1.ExecSandboxExit{ExitCode: 0}}})
}

func (s *remoteLookupTestServer) AttachTTY(stream grpc.BidiStreamingServer[runtimev1.MainProcessServiceAttachTTYRequest, runtimev1.MainProcessServiceAttachTTYResponse]) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	s.mainAttachStart = first.GetStart()
	for {
		if _, err := stream.Recv(); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
	}
	if err := stream.Send(&runtimev1.MainProcessServiceAttachTTYResponse{Payload: &runtimev1.MainProcessServiceAttachTTYResponse_Stdout{Stdout: []byte("foreground-output\n")}}); err != nil {
		return err
	}
	return stream.Send(&runtimev1.MainProcessServiceAttachTTYResponse{Payload: &runtimev1.MainProcessServiceAttachTTYResponse_ExitCode{ExitCode: 0}})
}

type remoteNativeCreateTestServer struct {
	*remoteLookupTestServer
	request     *gatewayv1.CreateSandboxRequest
	stagingPath string
}

func (s *remoteNativeCreateTestServer) BeginRootfsTarStaging(_ context.Context, request *gatewayv1.BeginRootfsTarStagingRequest) (*gatewayv1.BeginRootfsTarStagingResponse, error) {
	if request.GetWorkspaceScope().GetWorkspace() != "team" || request.GetFileName() != "rootfs.tar" || request.GetSizeBytes() == 0 {
		return nil, fmt.Errorf("unexpected staging request: %+v", request)
	}
	return &gatewayv1.BeginRootfsTarStagingResponse{StagingToken: "opaque-stage-token", UploadPath: s.stagingPath, MaxBytes: 1024}, nil
}

func (s *remoteNativeCreateTestServer) CreateSandbox(_ context.Context, request *gatewayv1.CreateSandboxRequest) (*gatewayv1.SandboxResponse, error) {
	s.request = request
	return &gatewayv1.SandboxResponse{Sandbox: &gatewayv1.Sandbox{Metadata: &datamodelv1.ObjectMeta{Name: request.GetName(), Workspace: request.GetWorkspaceScope().GetWorkspace()}, Spec: request.GetSpec()}, ServiceUrls: map[string]string{"api": "https://service.example"}}, nil
}

func (*remoteLookupTestServer) GetSandbox(_ context.Context, request *gatewayv1.GetSandboxRequest) (*gatewayv1.SandboxResponse, error) {
	return &gatewayv1.SandboxResponse{Sandbox: &gatewayv1.Sandbox{Metadata: &datamodelv1.ObjectMeta{Name: request.GetName(), Workspace: request.GetWorkspaceScope().GetWorkspace()}, Spec: &gatewayv1.SandboxSpec{Template: &gatewayv1.SandboxTemplate{Image: "alpine:3.22"}}, Status: &gatewayv1.SandboxStatus{Phase: gatewayv1.SandboxPhase_SANDBOX_PHASE_READY}}}, nil
}

func TestRemoteSandboxCreateValidatesContractBeforeGatewayAccess(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	app := &App{}
	for _, test := range []struct {
		name string
		opt  SandboxCreateOpts
		want string
	}{
		{name: "requires name", opt: SandboxCreateOpts{Image: "busybox"}, want: "--name is required"},
		{name: "requires image or template", opt: SandboxCreateOpts{Name: "agent"}, want: "provide --image"},
		{name: "rejects negative cpu before gateway", opt: SandboxCreateOpts{Name: "agent", Image: "busybox", CPU: -1}, want: "finite non-negative"},
		{name: "rejects excessive gpu count before gateway", opt: SandboxCreateOpts{Name: "agent", Image: "busybox", GPUCount: 1025}, want: "between 0 and 1024"},
		{name: "rejects template overrides", opt: SandboxCreateOpts{Name: "agent", Template: "shared", CPU: 1}, want: "--template cannot be combined with workload options"},
		{name: "rejects local-only alias image", opt: SandboxCreateOpts{Name: "agent", From: "base"}, want: "local-only image"},
		{name: "requires selected gateway", opt: SandboxCreateOpts{Name: "agent", Image: "busybox"}, want: "no current gateway"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := app.SandboxCreate(test.opt)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("SandboxCreate error=%v, want substring %q", err, test.want)
			}
		})
	}
}

func TestRemoteSandboxCreateCarriesWorkloadOptions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	created := &remoteNativeCreateTestServer{remoteLookupTestServer: &remoteLookupTestServer{}}
	gatewayv1.RegisterOpenShellServer(server, created)
	runtimev1.RegisterMainProcessServiceServer(server, created)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	app := &App{GatewayURLOverride: "http://" + listener.Addr().String(), GlobalWorkspace: "team"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	app.SetCommandContext(ctx)
	err = app.sandboxCreateOpenShell(SandboxCreateOpts{
		Name: "agent", Image: "alpine:3.22", CPU: .5, Memory: "2Gi", GPU: true, GPUCount: 2,
		Env: map[string]string{"FEATURE_FLAG": "on"}, DriverConfigJSON: `{"docker":{"shm_size":67108864,"cdi_devices":["nvidia.com/gpu=0","nvidia.com/gpu=1"]}}`,
		ForceTTY: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	resources := created.request.GetSpec().GetTemplate().GetResources().AsMap()["limits"].(map[string]any)
	if created.request == nil || created.request.GetWorkspaceScope().GetWorkspace() != "team" || resources["cpu"] != "500m" || resources["memory"] != "2147483648" || created.request.GetSpec().GetResourceRequirements().GetGpu().GetCount() != 2 || created.request.GetSpec().GetEnvironment()["FEATURE_FLAG"] != "on" || created.request.GetSpec().GetTemplate().GetDriverConfig().AsMap()["docker"] == nil || !created.request.GetSpec().GetTty() {
		t.Fatalf("remote create dropped workload options: %+v", created.request)
	}
}

func TestSandboxCreateAppliesLocalTemplateBeforeGatewayCreate(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := templates.Save(templates.Template{
		Name: "local-agent", Image: "alpine:3.22", CPU: 1.5, Memory: "2Gi", GPUCount: 1,
		DriverConfigJSON: `{"docker":{"shm_size":67108864}}`,
		Env:              map[string]string{"FROM_TEMPLATE": "yes"},
		Labels:           map[string]string{"source": "template"},
	}); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	created := &remoteNativeCreateTestServer{remoteLookupTestServer: &remoteLookupTestServer{}}
	gatewayv1.RegisterOpenShellServer(server, created)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	app := &App{GatewayURLOverride: "http://" + listener.Addr().String(), GlobalWorkspace: "team"}
	if err := app.SandboxCreate(SandboxCreateOpts{Name: "agent", Template: "local-agent", Detach: true}); err != nil {
		t.Fatal(err)
	}
	if created.request == nil {
		t.Fatal("Gateway create was not called")
	}
	spec := created.request.GetSpec().GetTemplate()
	if spec == nil || spec.GetImage() != "alpine:3.22" || created.request.GetWorkloadTemplate() != "" {
		t.Fatalf("local template was not expanded into the create request: request=%+v", created.request)
	}
	resources := spec.GetResources().AsMap()["limits"].(map[string]any)
	if resources["cpu"] != "1500m" || resources["memory"] != "2147483648" || created.request.GetSpec().GetResourceRequirements().GetGpu().GetCount() != 1 {
		t.Fatalf("local template resources were not applied: %+v", created.request)
	}
	if created.request.GetSpec().GetEnvironment()["FROM_TEMPLATE"] != "yes" || created.request.GetLabels()["source"] != "template" {
		t.Fatalf("local template env/labels were not applied: %+v", created.request)
	}
}

func TestSandboxCreateStagesRootfsTarIntoNativeVMDriverConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	stagingPath := filepath.Join(t.TempDir(), "staged-rootfs.tar")
	created := &remoteNativeCreateTestServer{remoteLookupTestServer: &remoteLookupTestServer{}, stagingPath: stagingPath}
	gatewayv1.RegisterOpenShellServer(server, created)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	rootfsPath := filepath.Join(t.TempDir(), "rootfs.tar")
	if err := os.WriteFile(rootfsPath, []byte("rootfs tar content"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := &App{GatewayURLOverride: "http://" + listener.Addr().String(), GlobalWorkspace: "team"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	app.SetCommandContext(ctx)
	if err := app.SandboxCreate(SandboxCreateOpts{Name: "vm-agent", From: rootfsPath, DriverConfigJSON: `{"vm":{"cpus":2}}`}); err != nil {
		t.Fatal(err)
	}
	staged, err := os.ReadFile(stagingPath)
	if err != nil || string(staged) != "rootfs tar content" {
		t.Fatalf("staged rootfs=%q error=%v", staged, err)
	}
	vmConfig := created.request.GetSpec().GetTemplate().GetDriverConfig().AsMap()["vm"].(map[string]any)
	if created.request.GetSpec().GetTemplate().GetImage() != "" || vmConfig["cpus"] != float64(2) || vmConfig[rootfsTarTokenField] != "opaque-stage-token" {
		t.Fatalf("rootfs CreateSandbox request=%+v VM config=%v", created.request, vmConfig)
	}
}

func TestRemoteSandboxCreateForegroundAttachesToCanonicalProcess(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	created := &remoteNativeCreateTestServer{remoteLookupTestServer: &remoteLookupTestServer{}}
	gatewayv1.RegisterOpenShellServer(server, created)
	runtimev1.RegisterMainProcessServiceServer(server, created)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	app := &App{GatewayURLOverride: "http://" + listener.Addr().String(), GlobalWorkspace: "team"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	app.SetCommandContext(ctx)
	if err := app.sandboxCreateOpenShell(SandboxCreateOpts{Name: "agent", Image: "alpine:3.22", Argv: []string{"sh", "-c", "printf hello"}, ForceTTY: true}); err != nil {
		t.Fatal(err)
	}
	if created.request == nil || !created.request.GetAwaitMainProcessAttachment() || len(created.request.GetSpec().GetCommand()) != 3 || len(created.commands) != 0 || created.mainAttachStart == nil || created.mainAttachStart.GetWorkspace() != "team" || created.mainAttachStart.GetSandbox() != "agent" {
		t.Fatalf("foreground remote create request=%+v exec commands=%v main attach=%+v", created.request, created.commands, created.mainAttachStart)
	}
}

func TestRemoteSandboxCreateCallsGatewayControlAndKeepsTemplateScope(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	created := &remoteNativeCreateTestServer{remoteLookupTestServer: &remoteLookupTestServer{}}
	gatewayv1.RegisterOpenShellServer(server, created)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	app := &App{GatewayURLOverride: "http://" + listener.Addr().String(), GlobalWorkspace: "team"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	app.SetCommandContext(ctx)
	err = app.SandboxCreate(SandboxCreateOpts{
		Name: "agent", Template: "shared-agent", Detach: true,
		Argv: []string{"sleep", "infinity"}, Labels: map[string]string{"purpose": "test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.request == nil || created.request.GetWorkspaceScope().GetWorkspace() != "team" || created.request.GetName() != "agent" || created.request.GetWorkloadTemplate() != "shared-agent" || created.request.GetSpec().GetTemplate() != nil {
		t.Fatalf("unexpected remote create request: %+v", created.request)
	}
	if len(created.request.GetSpec().GetCommand()) != 2 || created.request.GetSpec().GetCommand()[0] != "sleep" || created.request.GetLabels()["purpose"] != "test" {
		t.Fatalf("remote create request lost command/labels: %+v", created.request)
	}
}
