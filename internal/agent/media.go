package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type MediaProvider struct {
	Name               string
	BaseURL            string
	APIKey             string
	ImageModel         string
	VideoModel         string
	SpeechModel        string
	TranscriptionModel string
}

type MediaManager struct {
	Provider MediaProvider
	Client   *http.Client
}

type MediaResult struct {
	Path      string           `json:"path"`
	MediaType string           `json:"media_type"`
	Artifact  ArtifactManifest `json:"artifact"`
	Text      string           `json:"text,omitempty"`
}

func (p MediaProvider) Validate() error {
	if strings.TrimSpace(p.BaseURL) == "" {
		return errors.New("media provider base URL is required")
	}
	u, err := url.Parse(p.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && !(u.Scheme == "http" && isLoopbackHost(u.Hostname()))) {
		return errors.New("media provider must use HTTPS or a loopback HTTP endpoint")
	}
	if u.Scheme == "https" && strings.TrimSpace(p.APIKey) == "" {
		return errors.New("media provider API key is required")
	}
	return nil
}

func isLoopbackHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func NewMediaManager(provider MediaProvider) (*MediaManager, error) {
	if err := provider.Validate(); err != nil {
		return nil, err
	}
	return &MediaManager{Provider: provider, Client: &http.Client{Timeout: 3 * time.Minute}}, nil
}

func (m *MediaManager) GenerateImage(ctx context.Context, workspace, prompt, model string) (MediaResult, error) {
	if strings.TrimSpace(prompt) == "" {
		return MediaResult{}, errors.New("image prompt is required")
	}
	if model == "" {
		model = m.Provider.ImageModel
	}
	payload, err := m.postJSON(ctx, "/images/generations", map[string]any{"model": model, "prompt": prompt, "n": 1, "response_format": "b64_json"})
	if err != nil {
		return MediaResult{}, err
	}
	entry, err := firstData(payload)
	if err != nil {
		return MediaResult{}, err
	}
	path, mediaType, err := m.materializeEntry(ctx, workspace, "generated-image", ".png", entry)
	if err != nil {
		return MediaResult{}, err
	}
	artifact, err := BuildArtifactManifest(workspace, "", "", filepath.Base(path), filepath.ToSlash(filepath.Join(".agent-media", filepath.Base(path))))
	if err != nil {
		return MediaResult{}, err
	}
	return MediaResult{Path: path, MediaType: mediaType, Artifact: artifact}, nil
}

func (m *MediaManager) GenerateVideo(ctx context.Context, workspace, prompt, model string) (MediaResult, error) {
	if strings.TrimSpace(prompt) == "" {
		return MediaResult{}, errors.New("video prompt is required")
	}
	if model == "" {
		model = m.Provider.VideoModel
	}
	payload, err := m.postJSON(ctx, "/videos/generations", map[string]any{"model": model, "prompt": prompt})
	if err != nil {
		return MediaResult{}, err
	}
	entry, err := firstData(payload)
	if err != nil {
		return MediaResult{}, err
	}
	path, mediaType, err := m.materializeEntry(ctx, workspace, "generated-video", ".mp4", entry)
	if err != nil {
		return MediaResult{}, err
	}
	artifact, err := BuildArtifactManifest(workspace, "", "", filepath.Base(path), filepath.ToSlash(filepath.Join(".agent-media", filepath.Base(path))))
	if err != nil {
		return MediaResult{}, err
	}
	return MediaResult{Path: path, MediaType: mediaType, Artifact: artifact}, nil
}

func (m *MediaManager) GenerateSpeech(ctx context.Context, workspace, text, voice, model string) (MediaResult, error) {
	if strings.TrimSpace(text) == "" {
		return MediaResult{}, errors.New("speech text is required")
	}
	if model == "" {
		model = m.Provider.SpeechModel
	}
	body, err := m.postBytes(ctx, "/audio/speech", map[string]any{"model": model, "input": text, "voice": voice, "response_format": "wav"})
	if err != nil {
		return MediaResult{}, err
	}
	path := filepath.Join(workspace, ".agent-media", "speech-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".wav")
	if err := writeLimitedFile(path, body, 100<<20); err != nil {
		return MediaResult{}, err
	}
	artifact, err := BuildArtifactManifest(workspace, "", "", filepath.Base(path), filepath.ToSlash(filepath.Join(".agent-media", filepath.Base(path))))
	if err != nil {
		return MediaResult{}, err
	}
	return MediaResult{Path: path, MediaType: "audio/wav", Artifact: artifact}, nil
}

func (m *MediaManager) Transcribe(ctx context.Context, workspace, inputPath, model string) (MediaResult, error) {
	if strings.TrimSpace(inputPath) == "" {
		return MediaResult{}, errors.New("audio input is required")
	}
	file, err := os.Open(inputPath)
	if err != nil {
		return MediaResult{}, err
	}
	defer file.Close()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filepath.Base(inputPath))
	if err != nil {
		return MediaResult{}, err
	}
	if _, err := io.CopyN(part, file, 100<<20); err != nil && !errors.Is(err, io.EOF) {
		return MediaResult{}, err
	}
	if model == "" {
		model = m.Provider.TranscriptionModel
	}
	_ = writer.WriteField("model", model)
	if err := writer.Close(); err != nil {
		return MediaResult{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoint("/audio/transcriptions"), &body)
	if err != nil {
		return MediaResult{}, err
	}
	m.headers(request)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := m.client().Do(request)
	if err != nil {
		return MediaResult{}, err
	}
	defer response.Body.Close()
	payload, err := decodeResponse(response)
	if err != nil {
		return MediaResult{}, err
	}
	text, _ := payload["text"].(string)
	transcriptPath := filepath.Join(workspace, ".agent-media", "transcript-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".txt")
	if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o700); err != nil {
		return MediaResult{}, err
	}
	if err := os.WriteFile(transcriptPath, []byte(text+"\n"), 0o600); err != nil {
		return MediaResult{}, err
	}
	artifact, err := BuildArtifactManifest(workspace, "", "", filepath.Base(transcriptPath), filepath.ToSlash(filepath.Join(".agent-media", filepath.Base(transcriptPath))))
	if err != nil {
		return MediaResult{}, err
	}
	return MediaResult{Path: transcriptPath, MediaType: "text/plain", Text: text, Artifact: artifact}, nil
}

// AnalyzeImage sends an image to a vision-capable chat model. It supports both
// hosted OpenAI-compatible providers and a loopback Ollama /v1 endpoint.
func (m *MediaManager) AnalyzeImage(ctx context.Context, workspace, inputPath, prompt, model string) (MediaResult, error) {
	if strings.TrimSpace(inputPath) == "" || strings.TrimSpace(prompt) == "" {
		return MediaResult{}, errors.New("image input and vision prompt are required")
	}
	root, err := filepath.Abs(workspace)
	if err != nil {
		return MediaResult{}, err
	}
	input, err := filepath.Abs(inputPath)
	if err != nil {
		return MediaResult{}, err
	}
	relative, err := filepath.Rel(root, input)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return MediaResult{}, errors.New("image input escapes workspace")
	}
	data, err := os.ReadFile(input)
	if err != nil {
		return MediaResult{}, err
	}
	if len(data) > 25<<20 {
		return MediaResult{}, errors.New("image input exceeds 25 MiB")
	}
	if model == "" {
		model = m.Provider.ImageModel
	}
	mimeType := http.DetectContentType(data)
	payload, err := m.postJSON(ctx, "/chat/completions", map[string]any{
		"model": model,
		"messages": []any{map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": prompt},
			map[string]any{"type": "image_url", "image_url": map[string]string{"url": "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)}},
		}}},
	})
	if err != nil {
		return MediaResult{}, err
	}
	text := ""
	if choices, ok := payload["choices"].([]any); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]any); ok {
			if message, ok := choice["message"].(map[string]any); ok {
				text, _ = message["content"].(string)
			}
		}
	}
	if strings.TrimSpace(text) == "" {
		return MediaResult{}, errors.New("vision provider returned no text")
	}
	output := filepath.Join(workspace, ".agent-media", "vision-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".txt")
	if err := writeLimitedFile(output, []byte(text+"\n"), 4<<20); err != nil {
		return MediaResult{}, err
	}
	artifact, err := BuildArtifactManifest(workspace, "", "", filepath.Base(output), filepath.ToSlash(filepath.Join(".agent-media", filepath.Base(output))))
	if err != nil {
		return MediaResult{}, err
	}
	return MediaResult{Path: output, MediaType: "text/plain", Text: text, Artifact: artifact}, nil
}

func GenerateTone(workspace string, frequency float64, duration time.Duration) (MediaResult, error) {
	if frequency <= 0 || frequency > 20000 {
		frequency = 440
	}
	if duration <= 0 || duration > 30*time.Second {
		duration = time.Second
	}
	const sampleRate = 8000
	samples := int(duration.Seconds() * sampleRate)
	path := filepath.Join(workspace, ".agent-media", "tone-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".wav")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return MediaResult{}, err
	}
	data := make([]byte, samples*2)
	for i := 0; i < samples; i++ {
		sample := int16(12000 * sin(2*3.141592653589793*frequency*float64(i)/sampleRate))
		data[i*2] = byte(sample)
		data[i*2+1] = byte(sample >> 8)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return MediaResult{}, err
	}
	defer file.Close()
	if err := writeWAVHeader(file, len(data), sampleRate); err != nil {
		return MediaResult{}, err
	}
	if _, err := file.Write(data); err != nil {
		return MediaResult{}, err
	}
	artifact, err := BuildArtifactManifest(workspace, "", "", filepath.Base(path), filepath.ToSlash(filepath.Join(".agent-media", filepath.Base(path))))
	if err != nil {
		return MediaResult{}, err
	}
	return MediaResult{Path: path, MediaType: "audio/wav", Artifact: artifact}, nil
}

func (m *MediaManager) postJSON(ctx context.Context, path string, value any) (map[string]any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoint(path), bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	m.headers(request)
	request.Header.Set("Content-Type", "application/json")
	response, err := m.client().Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	return decodeResponse(response)
}

func (m *MediaManager) postBytes(ctx context.Context, path string, value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoint(path), bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	m.headers(request)
	request.Header.Set("Content-Type", "application/json")
	response, err := m.client().Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		return nil, fmt.Errorf("media request failed with status %d: %s", response.StatusCode, limitError(string(payload), 1000))
	}
	return io.ReadAll(io.LimitReader(response.Body, 100<<20))
}

func (m *MediaManager) materializeEntry(ctx context.Context, workspace, prefix, extension string, entry map[string]any) (string, string, error) {
	var data []byte
	mediaType := "application/octet-stream"
	if encoded, ok := entry["b64_json"].(string); ok && encoded != "" {
		var err error
		data, err = base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return "", "", err
		}
	}
	if rawURL, ok := entry["url"].(string); ok && rawURL != "" {
		parsed, err := url.Parse(rawURL)
		if err != nil || parsed.Scheme != "https" {
			return "", "", errors.New("media URL must use HTTPS")
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return "", "", err
		}
		response, err := m.client().Do(request)
		if err != nil {
			return "", "", err
		}
		defer response.Body.Close()
		if response.StatusCode/100 != 2 {
			return "", "", fmt.Errorf("media download failed with status %d", response.StatusCode)
		}
		data, err = io.ReadAll(io.LimitReader(response.Body, 100<<20))
		if err != nil {
			return "", "", err
		}
		mediaType = response.Header.Get("Content-Type")
	}
	if len(data) == 0 {
		return "", "", errors.New("media provider returned no data")
	}
	path := filepath.Join(workspace, ".agent-media", prefix+"-"+strconv.FormatInt(time.Now().UnixNano(), 10)+extension)
	if err := writeLimitedFile(path, data, 100<<20); err != nil {
		return "", "", err
	}
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	return path, mediaType, nil
}

func (m *MediaManager) endpoint(path string) string {
	return strings.TrimRight(m.Provider.BaseURL, "/") + "/" + strings.TrimLeft(path, "/")
}
func (m *MediaManager) client() *http.Client {
	if m.Client != nil {
		return m.Client
	}
	return http.DefaultClient
}
func (m *MediaManager) headers(request *http.Request) {
	if strings.TrimSpace(m.Provider.APIKey) != "" {
		request.Header.Set("Authorization", "Bearer "+m.Provider.APIKey)
	}
	request.Header.Set("User-Agent", "ollama-dz23-agentic-media/1")
}
func firstData(payload map[string]any) (map[string]any, error) {
	data, ok := payload["data"].([]any)
	if !ok || len(data) == 0 {
		return nil, errors.New("media response has no data")
	}
	entry, ok := data[0].(map[string]any)
	if !ok {
		return nil, errors.New("media response data is invalid")
	}
	return entry, nil
}
func decodeResponse(response *http.Response) (map[string]any, error) {
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if response.StatusCode/100 != 2 {
		return nil, fmt.Errorf("media request failed with status %d: %s", response.StatusCode, limitError(string(body), 1000))
	}
	return payload, nil
}
func writeLimitedFile(path string, data []byte, limit int64) error {
	if int64(len(data)) > limit {
		return errors.New("media payload exceeds limit")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
func sin(value float64) float64 {
	x := value
	for x > 3.141592653589793 {
		x -= 6.283185307179586
	}
	for x < -3.141592653589793 {
		x += 6.283185307179586
	}
	term, sum := x, x
	for n := 1; n < 8; n++ {
		term *= -x * x / float64((2*n)*(2*n+1))
		sum += term
	}
	return sum
}
func writeWAVHeader(w io.Writer, dataSize, sampleRate int) error {
	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	putLE32(header[4:8], uint32(36+dataSize))
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	putLE32(header[16:20], 16)
	putLE16(header[20:22], 1)
	putLE16(header[22:24], 1)
	putLE32(header[24:28], uint32(sampleRate))
	putLE32(header[28:32], uint32(sampleRate*2))
	putLE16(header[32:34], 2)
	putLE16(header[34:36], 16)
	copy(header[36:40], "data")
	putLE32(header[40:44], uint32(dataSize))
	_, err := w.Write(header)
	return err
}
func putLE16(b []byte, v uint16) { b[0] = byte(v); b[1] = byte(v >> 8) }
func putLE32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}
