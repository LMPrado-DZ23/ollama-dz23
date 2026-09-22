package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocumentIngestorChunksTextAndPersistsProvenance(t *testing.T) {
	root := t.TempDir()
	contextStore, err := NewContextStore(filepath.Join(root, "context"))
	if err != nil {
		t.Fatal(err)
	}
	project, err := contextStore.CreateProject("Docs", root)
	if err != nil {
		t.Fatal(err)
	}
	content := strings.Repeat("architecture evidence ", 200)
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	memories, err := (DocumentIngestor{Context: contextStore}).Ingest(context.Background(), DocumentIngestRequest{ProjectID: project.ID, Paths: []string{"notes.md"}, ChunkSize: 200, ChunkOverlap: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(memories) < 2 || !strings.HasPrefix(memories[0].Source, "notes.md#chunk-") {
		t.Fatalf("memories=%+v", memories)
	}
	if _, err := (DocumentIngestor{Context: contextStore}).Ingest(context.Background(), DocumentIngestRequest{ProjectID: project.ID, Paths: []string{"../outside.txt"}}); err == nil {
		t.Fatal("path traversal accepted")
	}
}
