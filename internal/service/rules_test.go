package service

import (
	"context"
	"net"
	"testing"

	gatewayv1 "github.com/KLORQ/quarshell-proto/gen/go/openshell/gateway/v1"
	"google.golang.org/grpc"
)

type policyTestServer struct {
	gatewayv1.UnimplementedOpenShellServer
	approved map[string]bool
}

func (s *policyTestServer) GetDraftPolicy(_ context.Context, _ *gatewayv1.GetDraftPolicyRequest) (*gatewayv1.GetDraftPolicyResponse, error) {
	return &gatewayv1.GetDraftPolicyResponse{Chunks: []*gatewayv1.PolicyChunk{
		{Id: "ordinary", Status: "pending", ReviewToken: "ordinary-review"},
		{Id: "flagged", Status: "pending", SecurityNotes: "security", ReviewToken: "flagged-review"},
	}}, nil
}
func (s *policyTestServer) ApproveDraftChunk(_ context.Context, req *gatewayv1.ApproveDraftChunkRequest) (*gatewayv1.ApproveDraftChunkResponse, error) {
	s.approved[req.ChunkId] = true
	return &gatewayv1.ApproveDraftChunkResponse{PolicyVersion: 2}, nil
}

func TestRuleApproveAllGatewayApprovesPendingWithoutClearing(t *testing.T) {
	approved := map[string]bool{}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	gatewayv1.RegisterOpenShellServer(server, &policyTestServer{approved: approved})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	a := &App{GatewayURLOverride: "http://" + listener.Addr().String()}
	if err := a.RuleApproveAll("demo", false); err != nil {
		t.Fatal(err)
	}
	if !approved["ordinary"] || approved["flagged"] {
		t.Fatalf("approved = %v", approved)
	}
	if err := a.RuleApproveAll("demo", true); err != nil {
		t.Fatal(err)
	}
	if !approved["flagged"] {
		t.Fatal("explicit opt-in did not approve flagged proposal")
	}
}
