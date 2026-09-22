package agent

import (
	"context"
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

type QueueStatus string

const (
	QueuePending    QueueStatus = "pending"
	QueueRunning    QueueStatus = "running"
	QueueSucceeded  QueueStatus = "succeeded"
	QueueFailed     QueueStatus = "failed"
	QueueDeadLetter QueueStatus = "dead_letter"
)

type QueueJob struct {
	ID          string      `json:"id"`
	MissionID   string      `json:"mission_id"`
	Status      QueueStatus `json:"status"`
	Attempts    int         `json:"attempts"`
	MaxAttempts int         `json:"max_attempts"`
	WorkerID    string      `json:"worker_id,omitempty"`
	AvailableAt time.Time   `json:"available_at"`
	LockedAt    *time.Time  `json:"locked_at,omitempty"`
	LastError   string      `json:"last_error,omitempty"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

type JobQueue struct {
	mu      sync.Mutex
	root    string
	jobs    map[string]QueueJob
	notify  chan struct{}
	started bool
}

func NewJobQueue(root string) (*JobQueue, error) {
	queue := &JobQueue{root: strings.TrimSpace(root), jobs: map[string]QueueJob{}, notify: make(chan struct{}, 1)}
	if queue.root == "" {
		return queue, nil
	}
	if err := os.MkdirAll(queue.root, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(queue.root, "jobs.json")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return queue, nil
	}
	var jobs map[string]QueueJob
	if err := readJSON(path, &jobs); err != nil {
		return nil, err
	}
	for id, job := range jobs {
		if job.Status == QueueRunning {
			job.Status = QueuePending
			job.WorkerID = ""
			job.LockedAt = nil
			job.AvailableAt = time.Now().UTC()
		}
		queue.jobs[id] = job
	}
	return queue, nil
}

func (q *JobQueue) Enqueue(missionID string, maxAttempts int) (QueueJob, error) {
	missionID = strings.TrimSpace(missionID)
	if missionID == "" {
		return QueueJob{}, errors.New("mission id is required")
	}
	if maxAttempts <= 0 || maxAttempts > 20 {
		maxAttempts = 3
	}
	now := time.Now().UTC()
	job := QueueJob{ID: "job_" + uuid.NewString(), MissionID: missionID, Status: QueuePending, MaxAttempts: maxAttempts, AvailableAt: now, CreatedAt: now, UpdatedAt: now}
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.persistLocked(job, true); err != nil {
		return QueueJob{}, err
	}
	q.signal()
	return job, nil
}

func (q *JobQueue) Claim(workerID string, now time.Time) (QueueJob, bool, error) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return QueueJob{}, false, errors.New("worker id is required")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	var candidate QueueJob
	found := false
	for _, job := range q.jobs {
		if job.Status != QueuePending || job.AvailableAt.After(now) {
			continue
		}
		if !found || job.AvailableAt.Before(candidate.AvailableAt) || job.CreatedAt.Before(candidate.CreatedAt) {
			candidate, found = job, true
		}
	}
	if !found {
		return QueueJob{}, false, nil
	}
	candidate.Status = QueueRunning
	candidate.Attempts++
	candidate.WorkerID = workerID
	locked := now
	candidate.LockedAt = &locked
	candidate.UpdatedAt = now
	if err := q.persistLocked(candidate, false); err != nil {
		return QueueJob{}, false, err
	}
	return candidate, true, nil
}

func (q *JobQueue) Ack(jobID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	job, ok := q.jobs[jobID]
	if !ok {
		return os.ErrNotExist
	}
	job.Status = QueueSucceeded
	job.WorkerID = ""
	job.UpdatedAt = time.Now().UTC()
	return q.persistLocked(job, false)
}

func (q *JobQueue) Nack(jobID string, runErr error) (QueueJob, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	job, ok := q.jobs[jobID]
	if !ok {
		return QueueJob{}, os.ErrNotExist
	}
	if runErr != nil {
		job.LastError = limitError(runErr.Error(), 2000)
	}
	job.WorkerID = ""
	job.LockedAt = nil
	job.UpdatedAt = time.Now().UTC()
	if job.Attempts >= job.MaxAttempts {
		job.Status = QueueDeadLetter
		return job, q.persistLocked(job, false)
	}
	job.Status = QueuePending
	backoff := time.Duration(1<<(job.Attempts-1)) * time.Second
	if backoff > 5*time.Minute {
		backoff = 5 * time.Minute
	}
	job.AvailableAt = time.Now().UTC().Add(backoff)
	return job, q.persistLocked(job, false)
}

func (q *JobQueue) Replay(jobID string) (QueueJob, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	job, ok := q.jobs[jobID]
	if !ok {
		return QueueJob{}, os.ErrNotExist
	}
	if job.Status != QueueDeadLetter && job.Status != QueueFailed {
		return QueueJob{}, fmt.Errorf("job %s is not replayable", jobID)
	}
	job.Status = QueuePending
	job.Attempts = 0
	job.LastError = ""
	job.WorkerID = ""
	job.LockedAt = nil
	job.AvailableAt = time.Now().UTC()
	job.UpdatedAt = time.Now().UTC()
	if err := q.persistLocked(job, false); err != nil {
		return QueueJob{}, err
	}
	q.signal()
	return job, nil
}

func (q *JobQueue) List(status QueueStatus) []QueueJob {
	q.mu.Lock()
	defer q.mu.Unlock()
	result := make([]QueueJob, 0, len(q.jobs))
	for _, job := range q.jobs {
		if status == "" || job.Status == status {
			result = append(result, job)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result
}

func (q *JobQueue) Start(ctx context.Context, workerID string, handler func(context.Context, QueueJob) error) {
	q.mu.Lock()
	if q.started {
		q.mu.Unlock()
		return
	}
	q.started = true
	q.mu.Unlock()
	go func() {
		for {
			job, ok, err := q.Claim(workerID, time.Now().UTC())
			if err == nil && ok {
				if runErr := handler(ctx, job); runErr != nil {
					_, _ = q.Nack(job.ID, runErr)
				} else {
					_ = q.Ack(job.ID)
				}
				continue
			}
			if ctx.Err() != nil {
				return
			}
			timer := time.NewTimer(500 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-q.notify:
				timer.Stop()
			case <-timer.C:
			}
		}
	}()
}

func (q *JobQueue) persistLocked(job QueueJob, add bool) error {
	if add {
		q.jobs[job.ID] = job
	} else {
		q.jobs[job.ID] = job
	}
	if q.root == "" {
		return nil
	}
	return writeJSONAtomic(filepath.Join(q.root, "jobs.json"), q.jobs)
}

func (q *JobQueue) signal() {
	select {
	case q.notify <- struct{}{}:
	default:
	}
}
func limitError(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
