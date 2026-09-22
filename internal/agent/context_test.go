package agent

import (
	"context"
	"testing"
)

type testEmbedder map[string][]float32

func (e testEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	return e[text], nil
}

func TestSemanticMemorySearchRanksByCosineSimilarity(t *testing.T) {
	store, err := NewContextStore("")
	if err != nil {
		t.Fatal(err)
	}
	store.SetEmbedder(testEmbedder{
		"alpha": {1, 0},
		"beta":  {0.8, 0.2},
		"query": {1, 0},
	})
	if _, err := store.AddMemoryContext(context.Background(), Memory{ProjectID: "project", Kind: "note", Content: "alpha", Confidence: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddMemoryContext(context.Background(), Memory{ProjectID: "project", Kind: "note", Content: "beta", Confidence: 1}); err != nil {
		t.Fatal(err)
	}
	memories, err := store.SearchMemoriesContext(context.Background(), "project", "query", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(memories) != 2 || memories[0].Content != "alpha" {
		t.Fatalf("memories = %+v", memories)
	}
}
