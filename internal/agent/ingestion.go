package agent

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type DocumentIngestRequest struct {
	ProjectID    string   `json:"project_id"`
	Workspace    string   `json:"workspace,omitempty"`
	Paths        []string `json:"paths,omitempty"`
	URLs         []string `json:"urls,omitempty"`
	ChunkSize    int      `json:"chunk_size,omitempty"`
	ChunkOverlap int      `json:"chunk_overlap,omitempty"`
	MaxBytes     int64    `json:"max_bytes,omitempty"`
}

type DocumentIngestor struct {
	Context  *ContextStore
	Research *ResearchEngine
}

func (i DocumentIngestor) Ingest(ctx context.Context, request DocumentIngestRequest) ([]Memory, error) {
	if i.Context == nil {
		return nil, errors.New("context store is required")
	}
	if strings.TrimSpace(request.ProjectID) == "" {
		return nil, errors.New("project_id is required")
	}
	project, err := i.Context.GetProject(request.ProjectID)
	if err != nil {
		return nil, err
	}
	root := project.Root
	if strings.TrimSpace(request.Workspace) != "" {
		root = request.Workspace
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if len(request.Paths)+len(request.URLs) == 0 {
		return nil, errors.New("at least one path or URL is required")
	}
	if len(request.Paths)+len(request.URLs) > 32 {
		return nil, errors.New("too many documents")
	}
	if request.ChunkSize <= 0 || request.ChunkSize > 12000 {
		request.ChunkSize = 1800
	}
	if request.ChunkOverlap < 0 || request.ChunkOverlap >= request.ChunkSize {
		request.ChunkOverlap = 200
	}
	if request.MaxBytes <= 0 || request.MaxBytes > 64<<20 {
		request.MaxBytes = 16 << 20
	}
	var memories []Memory
	var consumed int64
	for _, relative := range request.Paths {
		path, err := safeWorkspacePath(root, relative)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			continue
		}
		if consumed+info.Size() > request.MaxBytes {
			return nil, errors.New("ingestion byte budget exceeded")
		}
		text, err := readDocument(ctx, path, request.MaxBytes-consumed)
		if err != nil {
			return nil, fmt.Errorf("ingest %s: %w", relative, err)
		}
		consumed += info.Size()
		chunks := chunkText(text, request.ChunkSize, request.ChunkOverlap)
		for index, chunk := range chunks {
			memory, err := i.Context.AddMemoryContext(ctx, Memory{ProjectID: request.ProjectID, Kind: "document_chunk", Content: chunk, Source: relative + fmt.Sprintf("#chunk-%d", index+1), Confidence: 1})
			if err != nil {
				return nil, err
			}
			memories = append(memories, memory)
		}
	}
	for _, rawURL := range request.URLs {
		if i.Research == nil {
			return nil, errors.New("research engine is required for URL ingestion")
		}
		report, err := i.Research.Research(ctx, ResearchRequest{Query: rawURL, URLs: []string{rawURL}, MaxSources: 1, RespectRobots: true})
		if err != nil {
			return nil, err
		}
		for _, source := range report.Sources {
			if source.Error != "" {
				return nil, errors.New(source.Error)
			}
			for index, chunk := range chunkText(source.Text, request.ChunkSize, request.ChunkOverlap) {
				memory, err := i.Context.AddMemoryContext(ctx, Memory{ProjectID: request.ProjectID, Kind: "web_chunk", Content: chunk, Source: source.URL + fmt.Sprintf("#chunk-%d", index+1), Confidence: 0.8})
				if err != nil {
					return nil, err
				}
				memories = append(memories, memory)
			}
		}
	}
	return memories, nil
}

func readDocument(ctx context.Context, path string, limit int64) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".pdf":
		return readPDF(ctx, path, limit)
	case ".docx":
		return readDOCX(path, limit)
	case ".xlsx":
		return readXLSX(path, limit)
	case ".png", ".jpg", ".jpeg", ".webp", ".gif":
		return "", errors.New("image OCR requires a configured vision adapter")
	}
	data, err := readLimitedFile(path, limit)
	if err != nil {
		return "", err
	}
	if int64(len(data)) > limit {
		return "", errors.New("document exceeds byte budget")
	}
	if ext == ".html" || ext == ".htm" {
		_, text := extractResearchText(string(data), "text/html")
		return text, nil
	}
	return string(data), nil
}
func readPDF(ctx context.Context, path string, limit int64) (string, error) {
	command := exec.CommandContext(ctx, "pdftotext", "-layout", path, "-")
	var output bytes.Buffer
	command.Stdout = &limitedBuffer{Buffer: &output, Limit: int(limit)}
	err := command.Run()
	if err != nil {
		return "", fmt.Errorf("pdftotext: %w", err)
	}
	return output.String(), nil
}

func readLimitedFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("document exceeds byte budget")
	}
	return data, nil
}
func readDOCX(path string, limit int64) (string, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return "", err
	}
	defer archive.Close()
	for _, file := range archive.File {
		if file.Name != "word/document.xml" {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			return "", err
		}
		data, err := io.ReadAll(io.LimitReader(reader, limit))
		_ = reader.Close()
		if err != nil {
			return "", err
		}
		return xmlText(data), nil
	}
	return "", errors.New("docx document.xml not found")
}
func readXLSX(path string, limit int64) (string, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return "", err
	}
	defer archive.Close()
	var builder strings.Builder
	for _, file := range archive.File {
		if !strings.HasPrefix(file.Name, "xl/worksheets/") && !strings.HasSuffix(file.Name, "sharedStrings.xml") {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			return "", err
		}
		data, err := io.ReadAll(io.LimitReader(reader, limit))
		_ = reader.Close()
		if err != nil {
			return "", err
		}
		builder.WriteString(xmlText(data))
		builder.WriteByte('\n')
		if int64(builder.Len()) > limit {
			return "", errors.New("xlsx exceeds byte budget")
		}
	}
	if builder.Len() == 0 {
		return "", errors.New("xlsx contains no readable worksheets")
	}
	return builder.String(), nil
}
func xmlText(data []byte) string {
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	var builder strings.Builder
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		if character, ok := token.(xml.CharData); ok {
			builder.WriteString(string(character))
			builder.WriteByte(' ')
		}
	}
	return strings.TrimSpace(html.UnescapeString(builder.String()))
}
func chunkText(text string, size, overlap int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	runes := []rune(text)
	result := []string{}
	for start := 0; start < len(runes); {
		end := start + size
		if end > len(runes) {
			end = len(runes)
		}
		chunk := strings.TrimSpace(string(runes[start:end]))
		if chunk != "" {
			result = append(result, chunk)
		}
		if end == len(runes) {
			break
		}
		start = end - overlap
	}
	return result
}
