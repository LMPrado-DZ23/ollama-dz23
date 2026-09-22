package agent

import "testing"

func TestCollaborationPersistsCommentsAndPresence(t *testing.T) {
	root := t.TempDir()
	store, err := NewCollaborationStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddComment("project-1", "user-a", "revisar o preview"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetPresence("project-1", "user-a", "online"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewCollaborationStore(root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := reloaded.Snapshot("project-1")
	if len(snapshot.Comments) != 1 || len(snapshot.Presence) != 1 || snapshot.Comments[0].Body != "revisar o preview" {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}
