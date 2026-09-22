package agent

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Comment struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	UserID    string    `json:"user_id"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}
type Presence struct {
	ProjectID string    `json:"project_id"`
	UserID    string    `json:"user_id"`
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
}
type CollaborationSnapshot struct {
	Comments  []Comment  `json:"comments"`
	Presence  []Presence `json:"presence"`
	UpdatedAt time.Time  `json:"updated_at"`
}
type CollaborationStore struct {
	mu       sync.Mutex
	root     string
	comments map[string][]Comment
	presence map[string]map[string]Presence
}

func NewCollaborationStore(root string) (*CollaborationStore, error) {
	store := &CollaborationStore{root: root, comments: map[string][]Comment{}, presence: map[string]map[string]Presence{}}
	if strings.TrimSpace(root) == "" {
		return store, nil
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	_ = readJSON(filepath.Join(root, "comments.json"), &store.comments)
	_ = readJSON(filepath.Join(root, "presence.json"), &store.presence)
	return store, nil
}
func (s *CollaborationStore) AddComment(projectID, userID, body string) (Comment, error) {
	projectID = strings.TrimSpace(projectID)
	userID = strings.TrimSpace(userID)
	body = strings.TrimSpace(body)
	if projectID == "" || userID == "" || body == "" {
		return Comment{}, errors.New("project, user and comment are required")
	}
	if len(body) > 10000 {
		return Comment{}, errors.New("comment is too long")
	}
	comment := Comment{ID: "com_" + uuid.NewString(), ProjectID: projectID, UserID: userID, Body: body, CreatedAt: time.Now().UTC()}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.comments[projectID] = append(s.comments[projectID], comment)
	return comment, s.persistLocked()
}
func (s *CollaborationStore) Comments(projectID string) []Comment {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := append([]Comment(nil), s.comments[projectID]...)
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result
}
func (s *CollaborationStore) SetPresence(projectID, userID, status string) (Presence, error) {
	projectID = strings.TrimSpace(projectID)
	userID = strings.TrimSpace(userID)
	status = strings.TrimSpace(status)
	if projectID == "" || userID == "" {
		return Presence{}, errors.New("project and user are required")
	}
	if status == "" {
		status = "online"
	}
	presence := Presence{ProjectID: projectID, UserID: userID, Status: status, UpdatedAt: time.Now().UTC()}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.presence[projectID] == nil {
		s.presence[projectID] = map[string]Presence{}
	}
	s.presence[projectID][userID] = presence
	return presence, s.persistLocked()
}
func (s *CollaborationStore) Presence(projectID string) []Presence {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]Presence, 0, len(s.presence[projectID]))
	for _, item := range s.presence[projectID] {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].UserID < result[j].UserID })
	return result
}
func (s *CollaborationStore) Snapshot(projectID string) CollaborationSnapshot {
	return CollaborationSnapshot{Comments: s.Comments(projectID), Presence: s.Presence(projectID), UpdatedAt: time.Now().UTC()}
}
func (s *CollaborationStore) persistLocked() error {
	if s.root == "" {
		return nil
	}
	if err := writeJSONAtomic(filepath.Join(s.root, "comments.json"), s.comments); err != nil {
		return err
	}
	return writeJSONAtomic(filepath.Join(s.root, "presence.json"), s.presence)
}
