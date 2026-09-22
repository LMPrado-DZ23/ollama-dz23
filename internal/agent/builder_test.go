package agent

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuilderCreatesPreviewsExportsAndPublishes(t *testing.T) {
	service, err := NewBuilderService(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project, err := service.Create(context.Background(), BuilderSpec{Name: "Demo Game", Kind: BuilderGame, Files: map[string]string{"index.html": "<main>game</main>", "game.ts": "console.log('game')"}})
	if err != nil {
		t.Fatal(err)
	}
	preview, artifact, err := service.Preview(context.Background(), project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Status != "preview" || artifact.SHA256 == "" {
		t.Fatalf("preview=%+v artifact=%+v", preview, artifact)
	}
	_, archivePath, err := service.Export(context.Background(), project.ID)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	if len(archive.File) != 2 {
		t.Fatalf("archive files=%d", len(archive.File))
	}
	published, publishedPath, err := service.PublishLocal(context.Background(), project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if published.Status != "published" {
		t.Fatalf("published=%+v", published)
	}
	if _, err := os.Stat(filepath.Join(publishedPath, "index.html")); err != nil {
		t.Fatal(err)
	}
}

func TestBuilderRejectsTraversal(t *testing.T) {
	service, err := NewBuilderService(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), BuilderSpec{Name: "bad", Kind: BuilderWebsite, Files: map[string]string{"../secret": "no"}}); err == nil {
		t.Fatal("path traversal accepted")
	}
}

func TestBuilderVisualCanvasPersistsAndRenders(t *testing.T) {
	service, err := NewBuilderService(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project, err := service.Create(context.Background(), BuilderSpec{Name: "Visual", Kind: BuilderWebsite, Components: []VisualComponent{{ID: "hero", Type: "hero", Props: map[string]string{"text": "Hello"}, Width: 640, Height: 120}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(project.Components) != 1 {
		t.Fatalf("components=%+v", project.Components)
	}
	updated, err := service.ApplyVisualComponents(context.Background(), project.ID, []VisualComponent{{ID: "button", Type: "button", Props: map[string]string{"text": "Go"}}})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != project.Version+1 || len(updated.Components) != 1 {
		t.Fatalf("updated=%+v", updated)
	}
	data, err := os.ReadFile(filepath.Join(project.Root, project.Entry))
	if err != nil || !strings.Contains(string(data), "dz23-canvas") {
		t.Fatalf("visual preview invalid: err=%v", err)
	}
}

func TestBuilderVisualUndoRedo(t *testing.T) {
	root := t.TempDir()
	service, err := NewBuilderService(root)
	if err != nil {
		t.Fatal(err)
	}
	project, err := service.Create(context.Background(), BuilderSpec{Name: "History", Kind: BuilderWebsite, Components: []VisualComponent{{ID: "one", Type: "text"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyVisualComponents(context.Background(), project.ID, []VisualComponent{{ID: "two", Type: "button"}}); err != nil {
		t.Fatal(err)
	}
	undone, err := service.Undo(context.Background(), project.ID)
	if err != nil || undone.Components[0].ID != "one" {
		t.Fatalf("undo=%+v err=%v", undone, err)
	}
	redone, err := service.Redo(context.Background(), project.ID)
	if err != nil || redone.Components[0].ID != "two" {
		t.Fatalf("redo=%+v err=%v", redone, err)
	}
	reloaded, err := NewBuilderService(root)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := reloaded.Get(project.ID); err != nil || got.Components[0].ID != "two" {
		t.Fatalf("reloaded=%+v err=%v", got, err)
	}
}
