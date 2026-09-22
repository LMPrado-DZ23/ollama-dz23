package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Runtime struct {
	store         Store
	planner       Planner
	tools         *Registry
	workspaceRoot string
	context       *ContextStore
	metrics       *RuntimeMetrics
	connectors    *ConnectorManager
	mcp           *MCPManager
	queue         *JobQueue
	redisQueue    *RedisQueue
	traces        *TraceStore
	telemetry     *Telemetry
	media         *MediaManager
	builder       *BuilderService
	collaboration *CollaborationStore
	orchestrator  *AgentOrchestrator
	research      *ResearchEngine
	devices       *DeviceStore
	ingestion     DocumentIngestor
	push          *PushService
	deployments   *DeploymentManager
	mu            sync.Mutex
	running       map[string]bool
}

type RuntimeConfig struct {
	Store         Store
	Planner       Planner
	Tools         *Registry
	WorkspaceRoot string
	Context       *ContextStore
	Connectors    *ConnectorManager
	MCP           *MCPManager
	Queue         *JobQueue
	RedisQueue    *RedisQueue
	Traces        *TraceStore
	Telemetry     *Telemetry
	Media         *MediaManager
	Builder       *BuilderService
	Collaboration *CollaborationStore
	Devices       *DeviceStore
	Push          *PushService
	Deployments   *DeploymentManager
}

func NewRuntime(config RuntimeConfig) (*Runtime, error) {
	store := config.Store
	if store == nil {
		store = NewMemoryStore()
	}
	planner := config.Planner
	if planner == nil {
		planner = RulePlanner{}
	}
	tools := config.Tools
	if tools == nil {
		tools = NewRegistry()
	}
	if config.Connectors != nil {
		tools.Register(connectorTool{manager: config.Connectors})
	}
	if config.MCP != nil {
		tools.Register(mcpCallTool{manager: config.MCP})
	}
	root := config.WorkspaceRoot
	if strings.TrimSpace(root) == "" {
		root, _ = os.Getwd()
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	contextStore := config.Context
	if contextStore == nil {
		contextStore, err = NewContextStore(filepath.Join(root, ".agent-context"))
		if err != nil {
			return nil, err
		}
	}
	queue := config.Queue
	if queue == nil {
		queue, err = NewJobQueue(filepath.Join(root, ".agent-queue"))
		if err != nil {
			return nil, err
		}
	}
	traces := config.Traces
	if traces == nil {
		traces, err = NewTraceStore(filepath.Join(root, ".agent-traces"))
		if err != nil {
			return nil, err
		}
	}
	builder := config.Builder
	if builder == nil {
		builder, err = NewBuilderService(filepath.Join(root, ".agent-builders"))
		if err != nil {
			return nil, err
		}
	}
	collaboration := config.Collaboration
	if collaboration == nil {
		collaboration, err = NewCollaborationStore(filepath.Join(root, ".agent-collaboration"))
		if err != nil {
			return nil, err
		}
	}
	telemetry := config.Telemetry
	if telemetry == nil {
		telemetry, err = NewTelemetry(context.Background(), "")
		if err != nil {
			return nil, err
		}
	}
	runtime := &Runtime{store: store, planner: planner, tools: tools, workspaceRoot: root, context: contextStore, metrics: &RuntimeMetrics{}, connectors: config.Connectors, mcp: config.MCP, queue: queue, redisQueue: config.RedisQueue, traces: traces, telemetry: telemetry, media: config.Media, builder: builder, collaboration: collaboration, push: config.Push, deployments: config.Deployments, running: make(map[string]bool)}
	orchestrator, err := NewAgentOrchestrator(filepath.Join(root, ".agent-orchestrator"), runtime.SubagentRunner)
	if err != nil {
		return nil, err
	}
	runtime.orchestrator = orchestrator
	runtime.research = NewResearchEngine()
	devices := config.Devices
	if devices == nil {
		devices, err = NewDeviceStore(filepath.Join(root, ".agent-devices"))
		if err != nil {
			return nil, err
		}
	}
	runtime.devices = devices
	runtime.ingestion = DocumentIngestor{Context: contextStore, Research: runtime.research}
	return runtime, nil
}

// WithOrganization returns a request-scoped runtime view. Shared in-memory stores
// remain compatible, while PostgresStore receives a transaction-local RLS scope.
func (r *Runtime) WithOrganization(organizationID string) *Runtime {
	if r == nil {
		return nil
	}
	view := *r
	if postgres, ok := r.store.(*PostgresStore); ok {
		view.store = postgres.WithOrganization(organizationID)
	}
	return &view
}

func (r *Runtime) Context() *ContextStore {
	return r.context
}

func (r *Runtime) Metrics() MetricsSnapshot {
	return r.metrics.Snapshot()
}

func (r *Runtime) Connectors() []ConnectorConfig {
	if r.connectors == nil {
		return nil
	}
	return r.connectors.List()
}

func (r *Runtime) SetAuthStore(store *AuthStore) {
	if r != nil && r.connectors != nil {
		r.connectors.SetOAuthStore(store)
	}
}

func (r *Runtime) MCPServers() []MCPServerConfig {
	if r.mcp == nil {
		return nil
	}
	return r.mcp.List()
}

func (r *Runtime) Traces(traceID string) []TraceSpan {
	return r.traces.List(traceID, 500)
}

func (r *Runtime) Media() *MediaManager { return r.media }

func (r *Runtime) Builder() *BuilderService { return r.builder }

func (r *Runtime) Collaboration() *CollaborationStore { return r.collaboration }

func (r *Runtime) Orchestrator() *AgentOrchestrator { return r.orchestrator }

func (r *Runtime) Research() *ResearchEngine { return r.research }

func (r *Runtime) Devices() *DeviceStore { return r.devices }

func (r *Runtime) Ingestion() DocumentIngestor { return r.ingestion }

func (r *Runtime) Push() *PushService { return r.push }

func (r *Runtime) Deployments() *DeploymentManager { return r.deployments }

func (r *Runtime) CreateMission(ctx context.Context, request CreateMissionRequest) (Mission, error) {
	objective := strings.TrimSpace(request.Objective)
	if objective == "" {
		return Mission{}, errors.New("objective is required")
	}
	if len(objective) > 8<<10 {
		return Mission{}, errors.New("objective is too long")
	}
	workspace, err := r.resolveWorkspace(request.Workspace)
	if err != nil {
		return Mission{}, err
	}
	now := time.Now().UTC()
	mission := Mission{ID: "mis_" + uuid.NewString(), Version: 1, Objective: objective, Model: strings.TrimSpace(request.Model), Workspace: workspace, ProjectID: strings.TrimSpace(request.ProjectID), OrganizationID: strings.TrimSpace(request.OrganizationID), AutoRun: request.AutoRun, State: MissionPlanning, CreatedAt: now, UpdatedAt: now}
	if err := r.store.PutMission(mission); err != nil {
		return Mission{}, err
	}
	r.metrics.missionsCreated.Add(1)
	_ = r.event(mission, "mission.created", "", map[string]any{"objective": objective})
	plan, err := r.planner.Plan(ctx, mission)
	if err != nil {
		return r.failMission(mission, err)
	}
	plan, err = normalizeSteps(plan)
	if err != nil {
		return r.failMission(mission, err)
	}
	for index := range plan {
		tool, ok := r.tools.Get(plan[index].Kind)
		if !ok {
			return r.failMission(mission, fmt.Errorf("planner returned unregistered tool %q", plan[index].Kind))
		}
		descriptor := tool.Descriptor()
		plan[index].RequiresApproval = plan[index].RequiresApproval || descriptor.RequiresApproval
		if riskRank(descriptor.Risk) > riskRank(plan[index].Risk) {
			plan[index].Risk = descriptor.Risk
		}
	}
	mission.Plan = plan
	mission.State = MissionReady
	for _, step := range plan {
		if step.RequiresApproval {
			mission.Approvals = append(mission.Approvals, Approval{ID: "apr_" + uuid.NewString(), MissionID: mission.ID, StepID: step.ID, Status: ApprovalPending, CreatedAt: now, UpdatedAt: now})
		}
	}
	if len(mission.Approvals) > 0 {
		mission.State = MissionAwaitingApproval
	}
	mission.Version++
	mission.UpdatedAt = time.Now().UTC()
	if err := r.store.PutMission(mission); err != nil {
		return Mission{}, err
	}
	_ = r.event(mission, "mission.planned", "", map[string]any{"steps": len(plan), "approvals": len(mission.Approvals)})
	if request.AutoRun && mission.State == MissionReady {
		_, _ = r.EnqueueMission(mission.ID)
	}
	return mission, nil
}

func (r *Runtime) GetMission(id string) (Mission, error) {
	return r.store.GetMission(strings.TrimSpace(id))
}

func (r *Runtime) Start(ctx context.Context) {
	worker := func(jobContext context.Context, job QueueJob) error {
		return r.Run(jobContext, job.MissionID)
	}
	if r.redisQueue != nil {
		r.redisQueue.Start(ctx, "agent-runtime", worker)
	} else {
		r.queue.Start(ctx, "agent-runtime", worker)
	}
	go func() {
		r.resumePending(ctx)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.resumePending(ctx)
			}
		}
	}()
}

func (r *Runtime) resumePending(ctx context.Context) {
	for _, schedule := range r.context.ClaimDueSchedules(time.Now().UTC()) {
		_, _ = r.CreateMission(ctx, CreateMissionRequest{Objective: schedule.Objective, Model: schedule.Model, Workspace: schedule.Workspace, ProjectID: schedule.ProjectID, AutoRun: true})
	}
	missions, err := r.store.ListMissions()
	if err != nil {
		return
	}
	for _, mission := range missions {
		resume := mission.State == MissionRunning || mission.State == MissionRecovering || (mission.State == MissionReady && mission.AutoRun)
		if !resume || !r.approvalsReady(mission) {
			continue
		}
		_, _ = r.EnqueueMission(mission.ID)
	}
}

func (r *Runtime) EnqueueMission(missionID string) (QueueJob, error) {
	if _, err := r.store.GetMission(strings.TrimSpace(missionID)); err != nil {
		return QueueJob{}, err
	}
	if r.redisQueue != nil {
		return r.redisQueue.Enqueue(missionID, 3)
	}
	return r.queue.Enqueue(missionID, 3)
}

func (r *Runtime) QueueJobs(status QueueStatus) []QueueJob {
	if r.redisQueue != nil {
		return r.redisQueue.List(status)
	}
	return r.queue.List(status)
}

func (r *Runtime) ReplayJob(jobID string) (QueueJob, error) {
	if r.redisQueue != nil {
		return r.redisQueue.Replay(jobID)
	}
	return r.queue.Replay(jobID)
}

func (r *Runtime) ListTools() []ToolDescriptor {
	return r.tools.Descriptors()
}

func (r *Runtime) Run(ctx context.Context, id string) (runErr error) {
	ctx, otelSpan := r.telemetry.Start(ctx, "agent.mission.run", map[string]string{"mission.id": id})
	defer otelSpan.End()
	id = strings.TrimSpace(id)
	missionSpan := r.traces.Start("tr_"+id, "", "mission.run", map[string]any{"mission_id": id})
	defer func() { missionSpan.End("ok", runErr) }()
	if id == "" {
		return errors.New("mission id is required")
	}
	r.mu.Lock()
	if r.running[id] {
		r.mu.Unlock()
		return nil
	}
	r.running[id] = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.running, id)
		r.mu.Unlock()
	}()

	mission, err := r.store.GetMission(id)
	if err != nil {
		return err
	}
	if mission.State == MissionCompleted || mission.State == MissionCancelled {
		return nil
	}
	if !r.approvalsReady(mission) {
		mission.State = MissionAwaitingApproval
		mission.UpdatedAt = time.Now().UTC()
		_ = r.store.PutMission(mission)
		return nil
	}
	mission.State = MissionRunning
	mission.Version++
	mission.UpdatedAt = time.Now().UTC()
	if err := r.store.PutMission(mission); err != nil {
		return err
	}
	_ = r.event(mission, "mission.running", "", nil)

	for index := range mission.Plan {
		step := &mission.Plan[index]
		if step.State == StepSucceeded {
			continue
		}
		if step.RequiresApproval && !r.stepApproved(mission, step.ID) {
			step.State = StepBlocked
			mission.State = MissionAwaitingApproval
			mission.Version++
			mission.UpdatedAt = time.Now().UTC()
			_ = r.store.PutMission(mission)
			_ = r.event(mission, "step.awaiting_approval", step.ID, nil)
			return nil
		}
		tool, ok := r.tools.Get(step.Kind)
		if !ok {
			return r.failStep(mission, step, fmt.Errorf("tool %q is not registered", step.Kind))
		}
		step.State = StepRunning
		step.Attempts++
		r.metrics.stepsStarted.Add(1)
		r.metrics.toolCalls.Add(1)
		mission.State = MissionObserving
		mission.Version++
		mission.UpdatedAt = time.Now().UTC()
		if err := r.store.PutMission(mission); err != nil {
			return err
		}
		_ = r.event(mission, "step.started", step.ID, map[string]any{"tool": step.Kind, "attempt": step.Attempts})
		toolSpan := r.traces.Start("tr_"+mission.ID, missionSpan.ID(), "tool."+step.Kind, map[string]any{"mission_id": mission.ID, "step_id": step.ID, "tool": step.Kind})
		result, executeErr := tool.Execute(ctx, ToolContext{MissionID: mission.ID, StepID: step.ID, Workspace: mission.Workspace, OrganizationID: mission.OrganizationID}, step.Input)
		toolSpan.End("ok", executeErr)
		if executeErr != nil {
			if step.Attempts < 2 {
				r.metrics.retries.Add(1)
				step.State = StepPending
				mission.State = MissionRecovering
				mission.Version++
				mission.UpdatedAt = time.Now().UTC()
				_ = r.store.PutMission(mission)
				_ = r.event(mission, "step.retry_scheduled", step.ID, map[string]any{"error": executeErr.Error()})
				index--
				continue
			}
			return r.failStep(mission, step, executeErr)
		}
		step.State = StepSucceeded
		r.metrics.stepsSucceeded.Add(1)
		step.Result = result.Value
		step.Error = ""
		mission.Artifacts = append(mission.Artifacts, result.Artifacts...)
		mission.State = MissionRunning
		mission.Version++
		mission.UpdatedAt = time.Now().UTC()
		if err := r.store.PutMission(mission); err != nil {
			return err
		}
		_ = r.event(mission, "step.succeeded", step.ID, map[string]any{"artifacts": len(result.Artifacts)})
	}
	completed := time.Now().UTC()
	mission.State = MissionCompleted
	r.metrics.missionsCompleted.Add(1)
	mission.CompletedAt = &completed
	mission.Version++
	mission.UpdatedAt = completed
	if err := r.store.PutMission(mission); err != nil {
		return err
	}
	_ = r.event(mission, "mission.completed", "", map[string]any{"artifacts": len(mission.Artifacts)})
	return nil
}

func (r *Runtime) Cancel(id string) (Mission, error) {
	mission, err := r.store.GetMission(strings.TrimSpace(id))
	if err != nil {
		return Mission{}, err
	}
	if mission.State == MissionCompleted {
		return Mission{}, errors.New("completed mission cannot be cancelled")
	}
	mission.State = MissionCancelled
	mission.Version++
	mission.UpdatedAt = time.Now().UTC()
	if err := r.store.PutMission(mission); err != nil {
		return Mission{}, err
	}
	_ = r.event(mission, "mission.cancelled", "", nil)
	return mission, nil
}

func (r *Runtime) DecideApproval(missionID, approvalID string, approved bool, reason string) (Mission, error) {
	mission, err := r.store.GetMission(strings.TrimSpace(missionID))
	if err != nil {
		return Mission{}, err
	}
	for index := range mission.Approvals {
		if mission.Approvals[index].ID != approvalID {
			continue
		}
		if mission.Approvals[index].Status != ApprovalPending {
			continue
		}
		if approved {
			mission.Approvals[index].Status = ApprovalApproved
		} else {
			mission.Approvals[index].Status = ApprovalRejected
		}
		mission.Approvals[index].Reason = strings.TrimSpace(reason)
		mission.Approvals[index].UpdatedAt = time.Now().UTC()
		if approved && r.approvalsReady(mission) {
			mission.State = MissionReady
		} else if !approved {
			mission.State = MissionFailed
			mission.LastError = "approval rejected"
		}
		mission.Version++
		mission.UpdatedAt = time.Now().UTC()
		if err := r.store.PutMission(mission); err != nil {
			return Mission{}, err
		}
		r.metrics.approvals.Add(1)
		_ = r.event(mission, "approval.decided", mission.Approvals[index].StepID, map[string]any{"approved": approved, "reason": reason})
		return mission, nil
	}
	return Mission{}, errors.New("approval not found or already decided")
}

func (r *Runtime) Events(id string) ([]Event, error) {
	return r.store.ListEvents(strings.TrimSpace(id))
}

func (r *Runtime) Artifact(missionID, artifactID string) (ArtifactManifest, string, error) {
	mission, err := r.store.GetMission(strings.TrimSpace(missionID))
	if err != nil {
		return ArtifactManifest{}, "", err
	}
	for _, artifact := range mission.Artifacts {
		if artifact.ID != artifactID {
			continue
		}
		path, err := safeWorkspacePath(mission.Workspace, artifact.Path)
		if err != nil {
			return ArtifactManifest{}, "", err
		}
		return artifact, path, nil
	}
	return ArtifactManifest{}, "", os.ErrNotExist
}

func (r *Runtime) approvalsReady(mission Mission) bool {
	for _, approval := range mission.Approvals {
		if approval.Status != ApprovalApproved {
			return false
		}
	}
	return true
}

func (r *Runtime) stepApproved(mission Mission, stepID string) bool {
	for _, approval := range mission.Approvals {
		if approval.StepID == stepID {
			return approval.Status == ApprovalApproved
		}
	}
	return true
}

func (r *Runtime) failMission(mission Mission, err error) (Mission, error) {
	mission.State = MissionFailed
	r.metrics.missionsFailed.Add(1)
	mission.LastError = err.Error()
	mission.Version++
	mission.UpdatedAt = time.Now().UTC()
	if saveErr := r.store.PutMission(mission); saveErr != nil {
		return Mission{}, saveErr
	}
	_ = r.event(mission, "mission.failed", "", map[string]any{"error": err.Error()})
	return mission, err
}

func (r *Runtime) failStep(mission Mission, step *Step, err error) error {
	step.State = StepFailed
	r.metrics.stepsFailed.Add(1)
	step.Error = err.Error()
	mission.State = MissionFailed
	mission.LastError = err.Error()
	mission.Version++
	mission.UpdatedAt = time.Now().UTC()
	if saveErr := r.store.PutMission(mission); saveErr != nil {
		return saveErr
	}
	_ = r.event(mission, "step.failed", step.ID, map[string]any{"error": err.Error(), "attempts": step.Attempts})
	return err
}

func (r *Runtime) event(mission Mission, eventType, stepID string, payload any) error {
	err := r.store.AppendEvent(Event{ID: "evt_" + uuid.NewString(), MissionID: mission.ID, OrganizationID: mission.OrganizationID, Type: eventType, StepID: stepID, Payload: payload, CreatedAt: time.Now().UTC()})
	if r.push != nil && mission.OrganizationID != "" && (eventType == "mission.completed" || eventType == "mission.failed" || eventType == "step.awaiting_approval") {
		title := "DZ23 Agentic"
		body := "A missão " + mission.ID + " mudou de estado"
		if eventType == "mission.completed" {
			body = "A missão " + mission.ID + " foi concluída"
		}
		go func() {
			_ = r.push.NotifyOrganization(context.Background(), mission.OrganizationID, title, body, map[string]any{"mission_id": mission.ID, "event": eventType})
		}()
	}
	return err
}

func riskRank(risk RiskClass) int {
	switch risk {
	case RiskRead:
		return 0
	case RiskWrite:
		return 1
	case RiskExternalSideEffect:
		return 2
	case RiskDestructive:
		return 3
	default:
		return 2
	}
}

func (r *Runtime) resolveWorkspace(requested string) (string, error) {
	root, err := filepath.Abs(r.workspaceRoot)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(requested) == "" {
		return root, nil
	}
	candidate, err := filepath.Abs(requested)
	if err != nil {
		return "", err
	}
	if !isWithin(root, candidate) {
		return "", errors.New("workspace must be inside the configured agent root")
	}
	if err := os.MkdirAll(candidate, 0o700); err != nil {
		return "", err
	}
	return candidate, nil
}
