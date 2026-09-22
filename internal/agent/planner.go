package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ollama/ollama/api"
)

type Planner interface {
	Plan(ctx context.Context, mission Mission) ([]Step, error)
}

type RulePlanner struct{}

func (RulePlanner) Plan(_ context.Context, mission Mission) ([]Step, error) {
	objective := strings.ToLower(mission.Objective)
	if strings.Contains(objective, "escrever") || strings.Contains(objective, "criar arquivo") || strings.Contains(objective, "editar") {
		return []Step{{
			ID:               "step_1",
			Kind:             "workspace.write",
			Title:            "Escrever o resultado solicitado no workspace",
			Risk:             RiskWrite,
			RequiresApproval: true,
			State:            StepPending,
			Input:            map[string]any{"path": "agent-output.txt", "content": mission.Objective + "\n"},
		}}, nil
	}
	return []Step{{
		ID:    "step_1",
		Kind:  "workspace.list",
		Title: "Inspecionar o workspace autorizado",
		Risk:  RiskRead,
		State: StepPending,
		Input: map[string]any{"path": ".", "max_entries": 100},
	}}, nil
}

type OllamaPlanner struct {
	Client   *api.Client
	Model    string
	Fallback Planner
}

func (p OllamaPlanner) Plan(ctx context.Context, mission Mission) ([]Step, error) {
	if p.Client == nil || strings.TrimSpace(p.Model) == "" {
		return p.fallback().Plan(ctx, mission)
	}
	stream := false
	format := json.RawMessage(`"json"`)
	request := &api.ChatRequest{
		Model:  p.Model,
		Stream: &stream,
		Format: format,
		Messages: []api.Message{
			{Role: "system", Content: "You are a mission planner. Return only JSON with a top-level steps array. Each step must have kind, title, risk, requires_approval, and input. Allowed kinds are workspace.list, workspace.read, workspace.write, terminal.exec, sandbox.exec, browser.operator, desktop.companion, mcp.call, connector.http. Never invent completed results. Use read risk for inspection, write risk for filesystem changes, external_side_effect for browser, desktop, MCP, and connector actions, and require approval for write, terminal, sandbox, browser, desktop, MCP, or connector steps."},
			{Role: "user", Content: fmt.Sprintf("Objective: %s\nWorkspace: %s\nProject: %s", mission.Objective, mission.Workspace, mission.ProjectID)},
		},
	}
	var response string
	err := p.Client.Chat(ctx, request, func(chatResponse api.ChatResponse) error {
		response = chatResponse.Message.Content
		return nil
	})
	if err != nil {
		return p.fallback().Plan(ctx, mission)
	}
	steps, err := parsePlan(response)
	if err != nil {
		return p.fallback().Plan(ctx, mission)
	}
	return normalizeSteps(steps)
}

func (p OllamaPlanner) fallback() Planner {
	if p.Fallback != nil {
		return p.Fallback
	}
	return RulePlanner{}
}

func parsePlan(content string) ([]Step, error) {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	var payload struct {
		Steps []Step `json:"steps"`
	}
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return nil, err
	}
	if len(payload.Steps) == 0 {
		return nil, errors.New("planner returned no steps")
	}
	return payload.Steps, nil
}

func normalizeSteps(steps []Step) ([]Step, error) {
	if len(steps) == 0 || len(steps) > 32 {
		return nil, errors.New("planner step count is outside the allowed range")
	}
	allowed := map[string]RiskClass{
		"workspace.list":    RiskRead,
		"workspace.read":    RiskRead,
		"workspace.write":   RiskWrite,
		"terminal.exec":     RiskWrite,
		"sandbox.exec":      RiskWrite,
		"browser.operator":  RiskExternalSideEffect,
		"desktop.companion": RiskExternalSideEffect,
		"mcp.call":          RiskExternalSideEffect,
		"connector.http":    RiskExternalSideEffect,
	}
	for i := range steps {
		if _, ok := allowed[steps[i].Kind]; !ok {
			return nil, fmt.Errorf("planner returned unsupported tool %q", steps[i].Kind)
		}
		steps[i].ID = fmt.Sprintf("step_%d", i+1)
		if steps[i].Title == "" {
			steps[i].Title = steps[i].Kind
		}
		if steps[i].Risk == "" {
			steps[i].Risk = allowed[steps[i].Kind]
		}
		if steps[i].Risk != RiskRead {
			steps[i].RequiresApproval = true
		}
		steps[i].State = StepPending
	}
	return steps, nil
}
