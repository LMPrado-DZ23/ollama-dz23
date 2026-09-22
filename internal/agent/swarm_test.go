package agent

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAgentOrchestratorRunsSpecialistsAndSynthesizes(t *testing.T) {
	var active atomic.Int32
	var peak atomic.Int32
	var attempts atomic.Int32
	runner := func(ctx context.Context, task AgentTask) (AgentResult, error) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			old := peak.Load()
			if current <= old || peak.CompareAndSwap(old, current) {
				break
			}
		}
		if task.Role == RoleSecurity && attempts.Add(1) == 1 {
			return AgentResult{}, errors.New("transient")
		}
		select {
		case <-ctx.Done():
			return AgentResult{}, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
		return AgentResult{Output: string(task.Role) + " result", Evidence: []AgentEvidence{{URL: "https://example.test/source", SHA256: string(task.Role)}}}, nil
	}
	orchestrator, err := NewAgentOrchestrator(t.TempDir(), runner)
	if err != nil {
		t.Fatal(err)
	}
	job, err := orchestrator.Plan("pesquisar e revisar arquitetura", t.TempDir(), "project-1", []AgentRole{RoleResearch, RoleSecurity, RoleReview}, AgentBudget{MaxAgents: 2, MaxRetries: 1, MaxSeconds: 10})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := orchestrator.Run(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != OrchestrationCompleted || completed.Summary == "" {
		t.Fatalf("job=%+v", completed)
	}
	if peak.Load() > 2 {
		t.Fatalf("parallelism exceeded budget: %d", peak.Load())
	}
	if !strings.Contains(completed.Summary, "research") || len(completed.Conflicts) == 0 {
		t.Fatalf("summary=%q conflicts=%v", completed.Summary, completed.Conflicts)
	}
}

func TestAgentOrchestratorPersistsAndCancelsPlannedJob(t *testing.T) {
	root := t.TempDir()
	orchestrator, err := NewAgentOrchestrator(root, func(context.Context, AgentTask) (AgentResult, error) { return AgentResult{Output: "unused"}, nil })
	if err != nil {
		t.Fatal(err)
	}
	job, err := orchestrator.Plan("test", root, "", []AgentRole{RoleTesting}, AgentBudget{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := orchestrator.Cancel(job.ID); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewAgentOrchestrator(root, func(context.Context, AgentTask) (AgentResult, error) { return AgentResult{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	stored, err := reloaded.Get(job.ID)
	if err != nil || stored.State != OrchestrationCancelled {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
}
