// SPDX-FileCopyrightText: Copyright (c) 2026 quarshell
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/KLORQ/quarshell-sdk/go/klorq"
)

// RuleListFilter lists native OpenShell sandbox draft-policy chunks.
func (a *App) RuleListFilter(sandbox, status string) error {
	c, err := a.gatewayClient()
	if err != nil {
		return fmt.Errorf("rule list requires a configured Gateway: %w", err)
	}
	ctx, cancel := a.withTimeout(TimeoutAPI)
	defer cancel()
	sandboxes := []string{sandbox}
	if sandbox == "" {
		sandboxes = a.ruleCandidateSandboxes(ctx, c)
	}
	all := make([]map[string]any, 0)
	for _, name := range sandboxes {
		list, err := c.GetDraftPolicy(ctx, name, status)
		if err != nil {
			return fmt.Errorf("rule list sandbox %s: %w", name, err)
		}
		for _, chunk := range list.GetChunks() {
			all = append(all, map[string]any{"sandbox": name, "chunk": chunk})
		}
	}
	b, _ := json.MarshalIndent(all, "", "  ")
	fmt.Println(string(b))
	return nil
}

// RuleApprove approves a proposal, merges into sandbox base, writes live policy bind.
func (a *App) RuleApprove(id string) error {
	return a.ruleDecide(id, true, "")
}

// RuleApproveAll approves pending proposals, retaining security-flagged ones
// unless the caller explicitly opts in. It never clears proposal history.
func (a *App) RuleApproveAll(sandbox string, includeSecurityFlagged bool) error {
	gw, err := a.currentGatewayURL()
	if err != nil {
		return fmt.Errorf("rule approve-all requires a configured Gateway: %w", err)
	}
	if gw == "" {
		return fmt.Errorf("rule approve-all requires a configured Gateway")
	}
	c := a.clientWithToken(gw, a.gatewayTokenForURL(gw))
	ctx, cancel := a.withTimeout(TimeoutAPILong)
	defer cancel()
	sandboxes := []string{sandbox}
	if sandbox == "" {
		sandboxes = a.ruleCandidateSandboxes(ctx, c)
		if len(sandboxes) == 0 {
			return fmt.Errorf("rule approve-all: no sandboxes available")
		}
	}
	approved, skipped := 0, 0
	for _, sb := range sandboxes {
		draft, err := c.GetDraftPolicy(ctx, sb, "pending")
		if err != nil {
			return fmt.Errorf("rule approve-all: list %s: %w", sb, err)
		}
		pending := draft.GetChunks()
		sort.Slice(pending, func(i, j int) bool { return pending[i].GetId() < pending[j].GetId() })
		for _, chunk := range pending {
			if chunk.GetSecurityNotes() != "" && !includeSecurityFlagged {
				skipped++
				continue
			}
			if _, err := c.ApproveDraftChunk(ctx, sb, chunk.GetId(), chunk.GetReviewToken()); err != nil {
				return fmt.Errorf("rule approve-all: approved %d, failed %s: %w", approved, chunk.GetId(), err)
			}
			approved++
		}
	}
	fmt.Printf("rule approve-all: %d approved, %d security-flagged skipped\n", approved, skipped)
	return nil
}

// RuleRejectReason rejects a proposal with an optional reason.
func (a *App) RuleRejectReason(id, reason string) error {
	return a.ruleDecide(id, false, reason)
}

func (a *App) ruleDecide(id string, approve bool, reason string) error {
	gw, err := a.currentGatewayURL()
	if err != nil {
		return fmt.Errorf("rule decision requires a configured Gateway: %w", err)
	}
	if gw == "" {
		return fmt.Errorf("rule decision requires a configured Gateway")
	}
	c := a.clientWithToken(gw, a.gatewayTokenForURL(gw))
	ctx, cancel := a.withTimeout(TimeoutAPILong)
	defer cancel()

	sandbox := ""
	ruleName := ""
	reviewToken := ""
	found := false
	for _, sb := range a.ruleCandidateSandboxes(ctx, c) {
		draft, err := c.GetDraftPolicy(ctx, sb, "")
		if err != nil {
			continue
		}
		for _, chunk := range draft.GetChunks() {
			if chunk.GetId() == id {
				sandbox, ruleName, reviewToken, found = sb, chunk.GetRuleName(), chunk.GetReviewToken(), true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		return fmt.Errorf("policy draft chunk %q not found", id)
	}

	if approve {
		if _, err := c.ApproveDraftChunk(ctx, sandbox, id, reviewToken); err != nil {
			return err
		}
		fmt.Printf("rule approve: ok id=%s sandbox=%s rule=%s\n", id, sandbox, ruleName)
		return nil
	}
	if _, err := c.RejectDraftChunk(ctx, sandbox, id, reason); err != nil {
		return err
	}
	fmt.Printf("rule reject: ok id=%s sandbox=%s reason=%q\n", id, sandbox, reason)
	return nil
}

func (a *App) ruleCandidateSandboxes(ctx context.Context, c *klorq.Client) []string {
	var out []string
	if list, err := c.ListSandboxes(ctx); err == nil {
		for _, sb := range list {
			out = append(out, sb.Name)
		}
	}
	return out
}
