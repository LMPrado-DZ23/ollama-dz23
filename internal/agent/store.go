package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type Store interface {
	GetMission(id string) (Mission, error)
	ListMissions() ([]Mission, error)
	PutMission(mission Mission) error
	AppendEvent(event Event) error
	ListEvents(missionID string) ([]Event, error)
}

type JSONStore struct {
	mu         sync.RWMutex
	root       string
	missions   map[string]Mission
	events     map[string][]Event
	persistent bool
}

func NewJSONStore(root string) (*JSONStore, error) {
	if strings.TrimSpace(root) == "" {
		return NewMemoryStore(), nil
	}
	if err := os.MkdirAll(filepath.Join(root, "missions"), 0o700); err != nil {
		return nil, fmt.Errorf("create agent store: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "events"), 0o700); err != nil {
		return nil, fmt.Errorf("create agent event store: %w", err)
	}
	store := &JSONStore{root: root, missions: make(map[string]Mission), events: make(map[string][]Event), persistent: true}
	entries, err := os.ReadDir(filepath.Join(root, "missions"))
	if err != nil {
		return nil, fmt.Errorf("read agent missions: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		var mission Mission
		if err := readJSON(filepath.Join(root, "missions", entry.Name()), &mission); err != nil {
			return nil, fmt.Errorf("read mission %s: %w", id, err)
		}
		store.missions[id] = mission
	}
	return store, nil
}

func NewMemoryStore() *JSONStore {
	return &JSONStore{missions: make(map[string]Mission), events: make(map[string][]Event)}
}

func (s *JSONStore) GetMission(id string) (Mission, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	mission, ok := s.missions[id]
	if !ok {
		return Mission{}, os.ErrNotExist
	}
	return cloneMission(mission), nil
}

func (s *JSONStore) ListMissions() ([]Mission, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	missions := make([]Mission, 0, len(s.missions))
	for _, mission := range s.missions {
		missions = append(missions, cloneMission(mission))
	}
	sortMissions(missions)
	return missions, nil
}

func (s *JSONStore) PutMission(mission Mission) error {
	if strings.TrimSpace(mission.ID) == "" {
		return errors.New("mission id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.missions[mission.ID] = cloneMission(mission)
	if !s.persistent {
		return nil
	}
	path := filepath.Join(s.root, "missions", mission.ID+".json")
	return writeJSONAtomic(path, mission)
}

func (s *JSONStore) AppendEvent(event Event) error {
	if strings.TrimSpace(event.MissionID) == "" || strings.TrimSpace(event.ID) == "" {
		return errors.New("event id and mission id are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events[event.MissionID] = append(s.events[event.MissionID], event)
	if !s.persistent {
		return nil
	}
	path := filepath.Join(s.root, "events", event.MissionID+".json")
	return writeJSONAtomic(path, s.events[event.MissionID])
}

func (s *JSONStore) ListEvents(missionID string) ([]Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if events, ok := s.events[missionID]; ok {
		return append([]Event(nil), events...), nil
	}
	if !s.persistent {
		return []Event{}, nil
	}
	var events []Event
	path := filepath.Join(s.root, "events", missionID+".json")
	if err := readJSON(path, &events); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Event{}, nil
		}
		return nil, err
	}
	return events, nil
}

func writeJSONAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".agent-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func readJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func cloneMission(mission Mission) Mission {
	copy := mission
	copy.Plan = append([]Step(nil), mission.Plan...)
	for i := range copy.Plan {
		copy.Plan[i].Input = cloneMap(copy.Plan[i].Input)
	}
	copy.Approvals = append([]Approval(nil), mission.Approvals...)
	copy.Artifacts = append([]ArtifactManifest(nil), mission.Artifacts...)
	return copy
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func sortMissions(missions []Mission) {
	sort.Slice(missions, func(i, j int) bool { return missions[i].UpdatedAt.Before(missions[j].UpdatedAt) })
}
