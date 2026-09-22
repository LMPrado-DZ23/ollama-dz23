package agent

import (
	"context"
	"errors"
	"strings"

	"github.com/ollama/ollama/api"
)

type OllamaEmbedder struct {
	Client *api.Client
	Model  string
}

func (e OllamaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if e.Client == nil || strings.TrimSpace(e.Model) == "" {
		return nil, errors.New("ollama embedder requires client and model")
	}
	response, err := e.Client.Embed(ctx, &api.EmbedRequest{Model: e.Model, Input: text, Truncate: boolPointer(true)})
	if err != nil {
		return nil, err
	}
	if len(response.Embeddings) == 0 {
		return nil, errors.New("ollama embedder returned no vectors")
	}
	return response.Embeddings[0], nil
}

func boolPointer(value bool) *bool { return &value }
