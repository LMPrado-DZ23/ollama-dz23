package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ollama/ollama/envconfig"
)

func agentCommand() *cobra.Command {
	command := &cobra.Command{Use: "agent", Short: "Create and control agentic missions"}

	var objective, model, workspace, project string
	var autoRun bool
	create := &cobra.Command{
		Use:   "create",
		Short: "Create an agentic mission",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAgentRequest(cmd.Context(), http.MethodPost, "/api/agent/v1/missions", map[string]any{
				"objective":  objective,
				"model":      model,
				"workspace":  workspace,
				"project_id": project,
				"auto_run":   autoRun,
			})
		},
	}
	create.Flags().StringVar(&objective, "objective", "", "Mission objective")
	create.Flags().StringVar(&model, "model", "", "Planner model (optional)")
	create.Flags().StringVar(&workspace, "workspace", "", "Authorized workspace path")
	create.Flags().StringVar(&project, "project", "", "Project identifier")
	create.Flags().BoolVar(&autoRun, "auto-run", false, "Run immediately when no approval is required")
	_ = create.MarkFlagRequired("objective")

	idCommand := func(use, short, method, suffix string, body func() any) *cobra.Command {
		return &cobra.Command{Use: use, Short: short, Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentRequest(cmd.Context(), method, "/api/agent/v1/missions/"+url.PathEscape(args[0])+suffix, body())
		}}
	}
	get := idCommand("get MISSION_ID", "Show a mission", http.MethodGet, "", func() any { return nil })
	run := idCommand("run MISSION_ID", "Run a mission", http.MethodPost, "/run", func() any { return map[string]any{} })
	cancel := idCommand("cancel MISSION_ID", "Cancel a mission", http.MethodPost, "/cancel", func() any { return map[string]any{} })
	events := idCommand("events MISSION_ID", "Show mission events", http.MethodGet, "/events", func() any { return nil })
	tools := &cobra.Command{Use: "tools", Short: "List agent tools", RunE: func(cmd *cobra.Command, _ []string) error {
		return runAgentRequest(cmd.Context(), http.MethodGet, "/api/agent/v1/tools", nil)
	}}
	var approved bool
	var reason string
	approve := &cobra.Command{Use: "approve MISSION_ID APPROVAL_ID", Short: "Approve or reject a protected step", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		return runAgentRequest(cmd.Context(), http.MethodPost, "/api/agent/v1/missions/"+url.PathEscape(args[0])+"/approvals/"+url.PathEscape(args[1]), map[string]any{"approved": approved, "reason": reason})
	}}
	approve.Flags().BoolVar(&approved, "approved", false, "Approve the step; omit to reject")
	approve.Flags().StringVar(&reason, "reason", "", "Decision reason")

	command.AddCommand(create, get, run, cancel, events, approve, tools)
	return command
}

func runAgentRequest(ctx context.Context, method, path string, payload any) error {
	base := envconfig.Host()
	base.Path = strings.TrimSuffix(base.Path, "/")
	base.Path += path
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, base.String(), body)
	if err != nil {
		return err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("agent API %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	if len(data) == 0 {
		return nil
	}
	var pretty any
	if err := json.Unmarshal(data, &pretty); err == nil {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(pretty)
	}
	_, err = os.Stdout.Write(data)
	return err
}
