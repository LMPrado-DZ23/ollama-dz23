package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type ContextStore struct {
	mu        sync.RWMutex
	root      string
	projects  map[string]Project
	memories  map[string][]Memory
	skills    map[string]SkillManifest
	schedules map[string]Schedule
	embedder  Embedder
}

type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

func NewContextStore(root string) (*ContextStore, error) {
	if strings.TrimSpace(root) == "" {
		return &ContextStore{projects: map[string]Project{}, memories: map[string][]Memory{}, skills: map[string]SkillManifest{}, schedules: map[string]Schedule{}}, nil
	}
	if err := os.MkdirAll(filepath.Join(root, "projects"), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, "memories"), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, "schedules"), 0o700); err != nil {
		return nil, err
	}
	store := &ContextStore{root: root, projects: map[string]Project{}, memories: map[string][]Memory{}, skills: map[string]SkillManifest{}, schedules: map[string]Schedule{}}
	projectEntries, err := os.ReadDir(filepath.Join(root, "projects"))
	if err != nil {
		return nil, err
	}
	for _, entry := range projectEntries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		var project Project
		if err := readJSON(filepath.Join(root, "projects", entry.Name()), &project); err != nil {
			return nil, err
		}
		store.projects[project.ID] = project
	}
	memoryEntries, err := os.ReadDir(filepath.Join(root, "memories"))
	if err != nil {
		return nil, err
	}
	for _, entry := range memoryEntries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		var memories []Memory
		if err := readJSON(filepath.Join(root, "memories", entry.Name()), &memories); err != nil {
			return nil, err
		}
		projectID := strings.TrimSuffix(entry.Name(), ".json")
		store.memories[projectID] = memories
	}
	scheduleEntries, err := os.ReadDir(filepath.Join(root, "schedules"))
	if err != nil {
		return nil, err
	}
	for _, entry := range scheduleEntries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		var schedule Schedule
		if err := readJSON(filepath.Join(root, "schedules", entry.Name()), &schedule); err != nil {
			return nil, err
		}
		store.schedules[schedule.ID] = schedule
	}
	return store, nil
}

func (s *ContextStore) CreateProject(name, root string) (Project, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Project{}, errors.New("project name is required")
	}
	if len(name) > 200 {
		return Project{}, errors.New("project name is too long")
	}
	now := time.Now().UTC()
	project := Project{ID: "prj_" + uuid.NewString(), Name: name, Root: root, CreatedAt: now, UpdatedAt: now}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.projects[project.ID] = project
	if s.root != "" {
		if err := writeJSONAtomic(filepath.Join(s.root, "projects", project.ID+".json"), project); err != nil {
			return Project{}, err
		}
	}
	return project, nil
}

func (s *ContextStore) GetProject(id string) (Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	project, ok := s.projects[strings.TrimSpace(id)]
	if !ok {
		return Project{}, os.ErrNotExist
	}
	return project, nil
}

func (s *ContextStore) SetEmbedder(embedder Embedder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.embedder = embedder
}

func (s *ContextStore) AddMemory(memory Memory) (Memory, error) {
	return s.AddMemoryContext(context.Background(), memory)
}

func (s *ContextStore) AddMemoryContext(ctx context.Context, memory Memory) (Memory, error) {
	memory.Content = strings.TrimSpace(memory.Content)
	if memory.Content == "" {
		return Memory{}, errors.New("memory content is required")
	}
	if len(memory.Content) > 64<<10 {
		return Memory{}, errors.New("memory content is too long")
	}
	if memory.ID == "" {
		memory.ID = "mem_" + uuid.NewString()
	}
	if memory.CreatedAt.IsZero() {
		memory.CreatedAt = time.Now().UTC()
	}
	if memory.Confidence < 0 || memory.Confidence > 1 {
		return Memory{}, errors.New("memory confidence must be between 0 and 1")
	}
	s.mu.RLock()
	embedder := s.embedder
	s.mu.RUnlock()
	if embedder != nil && len(memory.Embedding) == 0 {
		embedding, err := embedder.Embed(ctx, memory.Content)
		if err != nil {
			return Memory{}, fmt.Errorf("embed memory: %w", err)
		}
		if len(embedding) == 0 || len(embedding) > 16384 {
			return Memory{}, errors.New("embedder returned an invalid vector")
		}
		memory.Embedding = embedding
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.memories[memory.ProjectID] = append(s.memories[memory.ProjectID], memory)
	if s.root != "" {
		if err := writeJSONAtomic(filepath.Join(s.root, "memories", memory.ProjectID+".json"), s.memories[memory.ProjectID]); err != nil {
			return Memory{}, err
		}
	}
	return memory, nil
}

func (s *ContextStore) SearchMemories(projectID, query string, limit int) []Memory {
	result, _ := s.SearchMemoriesContext(context.Background(), projectID, query, limit)
	return result
}

func (s *ContextStore) SearchMemoriesContext(ctx context.Context, projectID, query string, limit int) ([]Memory, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	s.mu.RLock()
	embedder := s.embedder
	memories := append([]Memory(nil), s.memories[projectID]...)
	s.mu.RUnlock()
	var queryVector []float32
	var err error
	if embedder != nil && query != "" {
		queryVector, err = embedder.Embed(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("embed query: %w", err)
		}
	}
	type scoredMemory struct {
		memory Memory
		score  float64
	}
	scored := make([]scoredMemory, 0, len(memories))
	for _, memory := range memories {
		score := float64(0)
		if len(queryVector) > 0 && len(memory.Embedding) > 0 {
			score = cosineSimilarity(queryVector, memory.Embedding)
		} else if query == "" || strings.Contains(strings.ToLower(memory.Content), query) || strings.Contains(strings.ToLower(memory.Kind), query) {
			score = 1
		} else {
			continue
		}
		scored = append(scored, scoredMemory{memory: memory, score: score})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].memory.CreatedAt.After(scored[j].memory.CreatedAt)
		}
		return scored[i].score > scored[j].score
	})
	matches := make([]Memory, 0, len(scored))
	for _, item := range scored {
		matches = append(matches, item.memory)
	}
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}

func cosineSimilarity(a, b []float32) float64 {
	length := len(a)
	if len(b) < length {
		length = len(b)
	}
	var dot, normA, normB float64
	for i := 0; i < length; i++ {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		normA += x * x
		normB += y * y
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

func (s *ContextStore) LoadSkills(dir string, trusted bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	loaded := make(map[string]SkillManifest)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return err
		}
		var manifest SkillManifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			return fmt.Errorf("skill %s: %w", entry.Name(), err)
		}
		if strings.TrimSpace(manifest.ID) == "" || strings.TrimSpace(manifest.Version) == "" {
			return fmt.Errorf("skill %s has no id or version", entry.Name())
		}
		manifest.Trusted = trusted
		loaded[manifest.ID] = manifest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, manifest := range loaded {
		s.skills[id] = manifest
	}
	return nil
}

func (s *ContextStore) Skills() []SkillManifest {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]SkillManifest, 0, len(s.skills))
	for _, skill := range s.skills {
		result = append(result, skill)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (s *ContextStore) CreateSchedule(schedule Schedule) (Schedule, error) {
	if strings.TrimSpace(schedule.Objective) == "" {
		return Schedule{}, errors.New("schedule objective is required")
	}
	if schedule.IntervalSeconds < 1 || schedule.IntervalSeconds > 31*24*60*60 {
		return Schedule{}, errors.New("schedule interval must be between 1 second and 31 days")
	}
	if schedule.ID == "" {
		schedule.ID = "sch_" + uuid.NewString()
	}
	now := time.Now().UTC()
	if schedule.NextRunAt.IsZero() || schedule.NextRunAt.Before(now) {
		schedule.NextRunAt = now.Add(time.Duration(schedule.IntervalSeconds) * time.Second)
	}
	schedule.Enabled = true
	schedule.CreatedAt = now
	schedule.UpdatedAt = now
	s.mu.Lock()
	defer s.mu.Unlock()
	s.schedules[schedule.ID] = schedule
	if s.root != "" {
		if err := writeJSONAtomic(filepath.Join(s.root, "schedules", schedule.ID+".json"), schedule); err != nil {
			return Schedule{}, err
		}
	}
	return schedule, nil
}

func (s *ContextStore) GetSchedule(id string) (Schedule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	schedule, ok := s.schedules[strings.TrimSpace(id)]
	if !ok {
		return Schedule{}, os.ErrNotExist
	}
	return schedule, nil
}

func (s *ContextStore) ListSchedules() []Schedule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Schedule, 0, len(s.schedules))
	for _, schedule := range s.schedules {
		result = append(result, schedule)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].NextRunAt.Before(result[j].NextRunAt) })
	return result
}

func (s *ContextStore) ClaimDueSchedules(now time.Time) []Schedule {
	s.mu.Lock()
	defer s.mu.Unlock()
	var due []Schedule
	for id, schedule := range s.schedules {
		if !schedule.Enabled || schedule.NextRunAt.After(now) {
			continue
		}
		due = append(due, schedule)
		last := now
		schedule.LastRunAt = &last
		schedule.NextRunAt = now.Add(time.Duration(schedule.IntervalSeconds) * time.Second)
		schedule.UpdatedAt = now
		s.schedules[id] = schedule
		if s.root != "" {
			_ = writeJSONAtomic(filepath.Join(s.root, "schedules", id+".json"), schedule)
		}
	}
	return due
}
