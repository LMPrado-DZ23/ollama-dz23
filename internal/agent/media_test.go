package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMediaManagerGeneratesArtifactsThroughHTTPSProvider(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/images/generations":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"b64_json": base64.StdEncoding.EncodeToString([]byte("png-bytes"))}}})
		case "/audio/transcriptions":
			_ = json.NewEncoder(w).Encode(map[string]any{"text": "transcrição aprovada"})
		case "/chat/completions":
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "OCR: texto visível"}}}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	manager, err := NewMediaManager(MediaProvider{Name: "test", BaseURL: server.URL, APIKey: "test-key", ImageModel: "image-test", TranscriptionModel: "stt-test"})
	if err != nil {
		t.Fatal(err)
	}
	manager.Client = server.Client()
	workspace := t.TempDir()
	image, err := manager.GenerateImage(context.Background(), workspace, "uma imagem de teste", "")
	if err != nil {
		t.Fatal(err)
	}
	if image.Artifact.SHA256 == "" || image.Artifact.Path == "" {
		t.Fatalf("invalid image artifact: %+v", image.Artifact)
	}
	input := filepath.Join(workspace, "input.wav")
	if err := os.WriteFile(input, []byte("wav"), 0o600); err != nil {
		t.Fatal(err)
	}
	transcript, err := manager.Transcribe(context.Background(), workspace, input, "")
	if err != nil || transcript.Text != "transcrição aprovada" {
		t.Fatalf("transcript=%+v err=%v", transcript, err)
	}
	inputImage := filepath.Join(workspace, "input.png")
	if err := os.WriteFile(inputImage, []byte("fake-png"), 0o600); err != nil {
		t.Fatal(err)
	}
	vision, err := manager.AnalyzeImage(context.Background(), workspace, inputImage, "Extraia o texto da imagem", "vision-test")
	if err != nil || vision.Text != "OCR: texto visível" {
		t.Fatalf("vision=%+v err=%v", vision, err)
	}
}

func TestGenerateToneProducesWAVArtifact(t *testing.T) {
	result, err := GenerateTone(t.TempDir(), 440, 200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 44 || string(data[:4]) != "RIFF" || result.MediaType != "audio/wav" {
		t.Fatalf("invalid wav: len=%d type=%s", len(data), result.MediaType)
	}
}
