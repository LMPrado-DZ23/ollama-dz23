package agent

import (
	"context"
	"time"
)

type MissionState string

const (
	MissionCreated          MissionState = "CREATED"
	MissionPlanning         MissionState = "PLANNING"
	MissionAwaitingApproval MissionState = "AWAITING_APPROVAL"
	MissionReady            MissionState = "READY"
	MissionRunning          MissionState = "RUNNING"
	MissionObserving        MissionState = "OBSERVING"
	MissionRecovering       MissionState = "RECOVERING"
	MissionCompleted        MissionState = "COMPLETED"
	MissionFailed           MissionState = "FAILED"
	MissionCancelled        MissionState = "CANCELLED"
)

type StepState string

const (
	StepPending   StepState = "PENDING"
	StepRunning   StepState = "RUNNING"
	StepSucceeded StepState = "SUCCEEDED"
	StepFailed    StepState = "FAILED"
	StepBlocked   StepState = "BLOCKED"
)

type ApprovalStatus string

const (
	ApprovalPending  ApprovalStatus = "PENDING"
	ApprovalApproved ApprovalStatus = "APPROVED"
	ApprovalRejected ApprovalStatus = "REJECTED"
)

type RiskClass string

const (
	RiskRead               RiskClass = "read"
	RiskWrite              RiskClass = "write"
	RiskExternalSideEffect RiskClass = "external_side_effect"
	RiskDestructive        RiskClass = "destructive"
)

type CreateMissionRequest struct {
	Objective      string `json:"objective"`
	Model          string `json:"model,omitempty"`
	Workspace      string `json:"workspace,omitempty"`
	ProjectID      string `json:"project_id,omitempty"`
	OrganizationID string `json:"organization_id,omitempty"`
	AutoRun        bool   `json:"auto_run,omitempty"`
}

type Mission struct {
	ID             string             `json:"id"`
	Version        int64              `json:"version"`
	Objective      string             `json:"objective"`
	Model          string             `json:"model,omitempty"`
	Workspace      string             `json:"workspace,omitempty"`
	ProjectID      string             `json:"project_id,omitempty"`
	OrganizationID string             `json:"organization_id,omitempty"`
	AutoRun        bool               `json:"auto_run,omitempty"`
	State          MissionState       `json:"state"`
	Plan           []Step             `json:"plan"`
	Approvals      []Approval         `json:"approvals,omitempty"`
	Artifacts      []ArtifactManifest `json:"artifacts,omitempty"`
	LastError      string             `json:"last_error,omitempty"`
	CreatedAt      time.Time          `json:"created_at"`
	UpdatedAt      time.Time          `json:"updated_at"`
	CompletedAt    *time.Time         `json:"completed_at,omitempty"`
}

type Step struct {
	ID               string         `json:"id"`
	Kind             string         `json:"kind"`
	Title            string         `json:"title"`
	Input            map[string]any `json:"input,omitempty"`
	Risk             RiskClass      `json:"risk"`
	RequiresApproval bool           `json:"requires_approval,omitempty"`
	State            StepState      `json:"state"`
	Attempts         int            `json:"attempts"`
	Result           any            `json:"result,omitempty"`
	Error            string         `json:"error,omitempty"`
}

type Approval struct {
	ID        string         `json:"id"`
	MissionID string         `json:"mission_id"`
	StepID    string         `json:"step_id"`
	Status    ApprovalStatus `json:"status"`
	Reason    string         `json:"reason,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type Event struct {
	ID             string    `json:"id"`
	MissionID      string    `json:"mission_id"`
	OrganizationID string    `json:"organization_id,omitempty"`
	Type           string    `json:"type"`
	StepID         string    `json:"step_id,omitempty"`
	Payload        any       `json:"payload,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

type ArtifactManifest struct {
	ID        string    `json:"id"`
	MissionID string    `json:"mission_id"`
	StepID    string    `json:"step_id,omitempty"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	MediaType string    `json:"media_type,omitempty"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256"`
	CreatedAt time.Time `json:"created_at"`
}

type ToolDescriptor struct {
	Name             string    `json:"name"`
	Version          string    `json:"version"`
	Description      string    `json:"description"`
	Risk             RiskClass `json:"risk"`
	Scopes           []string  `json:"scopes,omitempty"`
	RequiresApproval bool      `json:"requires_approval"`
}

type ToolContext struct {
	MissionID      string
	StepID         string
	Workspace      string
	OrganizationID string
}

type ToolResult struct {
	Value     any
	Artifacts []ArtifactManifest
}

type Tool interface {
	Descriptor() ToolDescriptor
	Execute(ctx context.Context, context ToolContext, input map[string]any) (ToolResult, error)
}

type Project struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Root      string    `json:"root"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Memory struct {
	ID         string    `json:"id"`
	ProjectID  string    `json:"project_id,omitempty"`
	Kind       string    `json:"kind"`
	Content    string    `json:"content"`
	Source     string    `json:"source,omitempty"`
	Confidence float64   `json:"confidence,omitempty"`
	Embedding  []float32 `json:"embedding,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type SkillManifest struct {
	ID          string   `json:"id"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Scopes      []string `json:"scopes,omitempty"`
	Tools       []string `json:"tools,omitempty"`
	Trusted     bool     `json:"trusted"`
}

type Schedule struct {
	ID               string     `json:"id"`
	Objective        string     `json:"objective"`
	Model            string     `json:"model,omitempty"`
	Workspace        string     `json:"workspace,omitempty"`
	ProjectID        string     `json:"project_id,omitempty"`
	IntervalSeconds  int64      `json:"interval_seconds"`
	Enabled          bool       `json:"enabled"`
	WebhookSecretEnv string     `json:"webhook_secret_env,omitempty"`
	NextRunAt        time.Time  `json:"next_run_at"`
	LastRunAt        *time.Time `json:"last_run_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}
