package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type AgentRole string

const (
	RoleResearch AgentRole = "research"
	RoleProgram  AgentRole = "programming"
	RoleTesting  AgentRole = "testing"
	RoleDesign   AgentRole = "design"
	RoleSecurity AgentRole = "security"
	RoleData     AgentRole = "data"
	RoleReview   AgentRole = "review"
)

type AgentTaskState string

const (
	AgentTaskPending   AgentTaskState = "PENDING"
	AgentTaskRunning   AgentTaskState = "RUNNING"
	AgentTaskSucceeded AgentTaskState = "SUCCEEDED"
	AgentTaskFailed    AgentTaskState = "FAILED"
	AgentTaskCancelled AgentTaskState = "CANCELLED"
)

type OrchestrationState string

const (
	OrchestrationPlanned   OrchestrationState = "PLANNED"
	OrchestrationRunning   OrchestrationState = "RUNNING"
	OrchestrationCompleted OrchestrationState = "COMPLETED"
	OrchestrationPartial   OrchestrationState = "COMPLETED_PARTIAL"
	OrchestrationFailed    OrchestrationState = "FAILED"
	OrchestrationCancelled OrchestrationState = "CANCELLED"
)

type AgentBudget struct {
	MaxAgents      int   `json:"max_agents"`
	MaxSeconds     int64 `json:"max_seconds"`
	MaxOutputBytes int   `json:"max_output_bytes"`
	MaxRetries     int   `json:"max_retries"`
}

type AgentEvidence struct {
	URL     string `json:"url,omitempty"`
	Title   string `json:"title,omitempty"`
	Excerpt string `json:"excerpt,omitempty"`
	Source  string `json:"source,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
}

type AgentTask struct {
	ID         string          `json:"id"`
	Role       AgentRole       `json:"role"`
	Objective  string          `json:"objective"`
	Workspace  string          `json:"workspace,omitempty"`
	ProjectID  string          `json:"project_id,omitempty"`
	State      AgentTaskState  `json:"state"`
	Attempts   int             `json:"attempts"`
	Output     string          `json:"output,omitempty"`
	Evidence   []AgentEvidence `json:"evidence,omitempty"`
	Error      string          `json:"error,omitempty"`
	StartedAt  *time.Time      `json:"started_at,omitempty"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
	DurationMS int64           `json:"duration_ms,omitempty"`
	TokensUsed int             `json:"tokens_used,omitempty"`
}

type AgentResult struct {
	Output     string          `json:"output,omitempty"`
	Evidence   []AgentEvidence `json:"evidence,omitempty"`
	TokensUsed int             `json:"tokens_used,omitempty"`
}

type OrchestrationJob struct {
	ID          string             `json:"id"`
	Objective   string             `json:"objective"`
	Workspace   string             `json:"workspace,omitempty"`
	ProjectID   string             `json:"project_id,omitempty"`
	State       OrchestrationState `json:"state"`
	Budget      AgentBudget        `json:"budget"`
	Tasks       []AgentTask        `json:"tasks"`
	Summary     string             `json:"summary,omitempty"`
	Conflicts   []string           `json:"conflicts,omitempty"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
	CompletedAt *time.Time         `json:"completed_at,omitempty"`
}

type SubagentRunner func(context.Context, AgentTask) (AgentResult, error)
type ResultReducer func(context.Context, OrchestrationJob) (string, []string, error)

type AgentOrchestrator struct {
	mu      sync.Mutex
	root    string
	runner  SubagentRunner
	reducer ResultReducer
	jobs    map[string]OrchestrationJob
}

func NewAgentOrchestrator(root string, runner SubagentRunner) (*AgentOrchestrator, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("orchestrator root is required")
	}
	if runner == nil {
		return nil, errors.New("orchestrator runner is required")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	o := &AgentOrchestrator{root: root, runner: runner, jobs: map[string]OrchestrationJob{}}
	if err := readJSON(filepath.Join(root, "jobs.json"), &o.jobs); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	o.reducer = defaultAgentReducer
	return o, nil
}

func PlanAgentTasks(objective, workspace, projectID string, roles []AgentRole) ([]AgentTask, error) {
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return nil, errors.New("orchestration objective is required")
	}
	if len(objective) > 16000 {
		return nil, errors.New("orchestration objective is too long")
	}
	if len(roles) == 0 {
		roles = inferAgentRoles(objective)
	}
	seen := map[AgentRole]bool{}
	tasks := make([]AgentTask, 0, len(roles))
	for _, role := range roles {
		if !validAgentRole(role) || seen[role] {
			continue
		}
		seen[role] = true
		tasks = append(tasks, AgentTask{ID: "agt_" + uuid.NewString(), Role: role, Objective: roleObjective(role, objective), Workspace: workspace, ProjectID: projectID, State: AgentTaskPending})
	}
	if len(tasks) == 0 || len(tasks) > 12 {
		return nil, errors.New("agent role count is outside the allowed range")
	}
	return tasks, nil
}

func (o *AgentOrchestrator) Plan(objective, workspace, projectID string, roles []AgentRole, budget AgentBudget) (OrchestrationJob, error) {
	tasks, err := PlanAgentTasks(objective, workspace, projectID, roles)
	if err != nil {
		return OrchestrationJob{}, err
	}
	budget = normalizeAgentBudget(budget, len(tasks))
	now := time.Now().UTC()
	job := OrchestrationJob{ID: "orch_" + uuid.NewString(), Objective: strings.TrimSpace(objective), Workspace: workspace, ProjectID: projectID, State: OrchestrationPlanned, Budget: budget, Tasks: tasks, CreatedAt: now, UpdatedAt: now}
	o.mu.Lock()
	o.jobs[job.ID] = job
	err = o.persistLocked()
	o.mu.Unlock()
	return job, err
}

func (o *AgentOrchestrator) Get(id string) (OrchestrationJob, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	job, ok := o.jobs[strings.TrimSpace(id)]
	if !ok {
		return OrchestrationJob{}, os.ErrNotExist
	}
	return job, nil
}

func (o *AgentOrchestrator) Run(ctx context.Context, id string) (OrchestrationJob, error) {
	o.mu.Lock()
	job, ok := o.jobs[strings.TrimSpace(id)]
	if !ok {
		o.mu.Unlock()
		return OrchestrationJob{}, os.ErrNotExist
	}
	if job.State == OrchestrationRunning {
		o.mu.Unlock()
		return job, errors.New("orchestration job is already running")
	}
	job.State = OrchestrationRunning
	job.UpdatedAt = time.Now().UTC()
	o.jobs[id] = job
	_ = o.persistLocked()
	o.mu.Unlock()
	if job.Budget.MaxSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(job.Budget.MaxSeconds)*time.Second)
		defer cancel()
	}
	semaphore := make(chan struct{}, job.Budget.MaxAgents)
	var wait sync.WaitGroup
	var taskMu sync.Mutex
	for index := range job.Tasks {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				taskMu.Lock()
				job.Tasks[index].State = AgentTaskCancelled
				taskMu.Unlock()
				return
			}
			defer func() { <-semaphore }()
			task := &job.Tasks[index]
			start := time.Now().UTC()
			taskMu.Lock()
			task.State = AgentTaskRunning
			task.StartedAt = &start
			taskMu.Unlock()
			var result AgentResult
			var runErr error
			for attempt := 1; attempt <= job.Budget.MaxRetries+1; attempt++ {
				taskMu.Lock()
				task.Attempts = attempt
				taskMu.Unlock()
				result, runErr = o.runner(ctx, *task)
				if runErr == nil {
					break
				}
				if ctx.Err() != nil {
					break
				}
			}
			finish := time.Now().UTC()
			taskMu.Lock()
			task.FinishedAt = &finish
			task.DurationMS = finish.Sub(start).Milliseconds()
			task.Output = truncateAgentOutput(result.Output, job.Budget.MaxOutputBytes)
			task.Evidence = result.Evidence
			task.TokensUsed = result.TokensUsed
			if ctx.Err() != nil {
				task.State = AgentTaskCancelled
				task.Error = ctx.Err().Error()
			} else if runErr != nil {
				task.State = AgentTaskFailed
				task.Error = runErr.Error()
			} else {
				task.State = AgentTaskSucceeded
			}
			taskMu.Unlock()
		}(index)
	}
	wait.Wait()
	if err := ctx.Err(); err != nil && !errors.Is(err, context.Canceled) {
		job.State = OrchestrationCancelled
	} else if errors.Is(ctx.Err(), context.Canceled) {
		job.State = OrchestrationCancelled
	} else {
		succeeded, failed := 0, 0
		for _, task := range job.Tasks {
			if task.State == AgentTaskSucceeded {
				succeeded++
			}
			if task.State == AgentTaskFailed {
				failed++
			}
		}
		if succeeded == 0 && failed > 0 {
			job.State = OrchestrationFailed
		} else if failed > 0 {
			job.State = OrchestrationPartial
		} else {
			job.State = OrchestrationCompleted
		}
	}
	if job.State == OrchestrationCompleted || job.State == OrchestrationPartial {
		summary, conflicts, err := o.reducer(ctx, job)
		if err != nil {
			job.State = OrchestrationPartial
			job.Conflicts = append(job.Conflicts, err.Error())
		} else {
			job.Summary, job.Conflicts = summary, conflicts
		}
	}
	now := time.Now().UTC()
	job.CompletedAt = &now
	job.UpdatedAt = now
	o.mu.Lock()
	o.jobs[id] = job
	err := o.persistLocked()
	o.mu.Unlock()
	return job, err
}

func (o *AgentOrchestrator) Cancel(id string) (OrchestrationJob, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	job, ok := o.jobs[strings.TrimSpace(id)]
	if !ok {
		return OrchestrationJob{}, os.ErrNotExist
	}
	if job.State == OrchestrationPlanned {
		job.State = OrchestrationCancelled
		job.UpdatedAt = time.Now().UTC()
		o.jobs[id] = job
		return job, o.persistLocked()
	}
	return job, errors.New("running cancellation requires the request context")
}

func (o *AgentOrchestrator) persistLocked() error {
	return writeJSONAtomic(filepath.Join(o.root, "jobs.json"), o.jobs)
}
func normalizeAgentBudget(b AgentBudget, taskCount int) AgentBudget {
	if b.MaxAgents <= 0 || b.MaxAgents > taskCount {
		b.MaxAgents = taskCount
	}
	if b.MaxSeconds <= 0 || b.MaxSeconds > 3600 {
		b.MaxSeconds = 600
	}
	if b.MaxOutputBytes <= 0 || b.MaxOutputBytes > 2<<20 {
		b.MaxOutputBytes = 128 << 10
	}
	if b.MaxRetries < 0 || b.MaxRetries > 3 {
		b.MaxRetries = 1
	}
	return b
}
func truncateAgentOutput(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "\n[output truncated]"
}
func validAgentRole(role AgentRole) bool {
	switch role {
	case RoleResearch, RoleProgram, RoleTesting, RoleDesign, RoleSecurity, RoleData, RoleReview:
		return true
	default:
		return false
	}
}
func inferAgentRoles(objective string) []AgentRole {
	lower := strings.ToLower(objective)
	roles := []AgentRole{}
	if strings.Contains(lower, "pesquis") || strings.Contains(lower, "research") || strings.Contains(lower, "compar") {
		roles = append(roles, RoleResearch)
	}
	if strings.Contains(lower, "dado") || strings.Contains(lower, "csv") || strings.Contains(lower, "métrica") {
		roles = append(roles, RoleData)
	}
	roles = append(roles, RoleProgram, RoleTesting, RoleSecurity, RoleReview)
	return roles
}
func roleObjective(role AgentRole, objective string) string {
	return fmt.Sprintf("Você é o subagente %s. Trabalhe de forma independente sobre o objetivo abaixo, registre evidências verificáveis, não invente resultados e entregue uma saída curta para síntese.\n\nObjetivo: %s", role, objective)
}

func defaultAgentReducer(_ context.Context, job OrchestrationJob) (string, []string, error) {
	tasks := append([]AgentTask(nil), job.Tasks...)
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Role < tasks[j].Role })
	var builder strings.Builder
	builder.WriteString("# Síntese multiagente\n\n")
	builder.WriteString("Objetivo: " + job.Objective + "\n\n")
	conflicts := []string{}
	evidenceByURL := map[string]string{}
	for _, task := range tasks {
		if task.State != AgentTaskSucceeded {
			continue
		}
		builder.WriteString("## " + string(task.Role) + "\n\n")
		builder.WriteString(task.Output + "\n\n")
		for _, evidence := range task.Evidence {
			if evidence.URL == "" {
				continue
			}
			if previous, ok := evidenceByURL[evidence.URL]; ok && previous != evidence.SHA256 {
				conflicts = append(conflicts, "evidence conflict at "+evidence.URL)
			} else {
				evidenceByURL[evidence.URL] = evidence.SHA256
			}
		}
	}
	if builder.Len() == len("# Síntese multiagente\n\nObjetivo: "+job.Objective+"\n\n") {
		return "", conflicts, errors.New("no successful subagent output")
	}
	return builder.String(), conflicts, nil
}

func (r *Runtime) SubagentRunner(ctx context.Context, task AgentTask) (AgentResult, error) {
	mission, err := r.CreateMission(ctx, CreateMissionRequest{Objective: task.Objective, Workspace: task.Workspace, ProjectID: task.ProjectID})
	if err != nil {
		return AgentResult{}, err
	}
	if err := r.Run(ctx, mission.ID); err != nil {
		return AgentResult{}, err
	}
	completed, err := r.GetMission(mission.ID)
	if err != nil {
		return AgentResult{}, err
	}
	data, _ := json.Marshal(completed)
	return AgentResult{Output: string(data)}, nil
}
