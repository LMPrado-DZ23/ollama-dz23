package agent

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
)

func OCRLocal(ctx context.Context, workspace, inputPath, language string) (MediaResult, error) {
	if strings.TrimSpace(inputPath) == "" {
		return MediaResult{}, errors.New("ocr input is required")
	}
	if _, err := exec.LookPath("tesseract"); err != nil {
		return MediaResult{}, errors.New("tesseract is not installed")
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
		return MediaResult{}, errors.New("ocr input escapes workspace")
	}
	language = strings.TrimSpace(language)
	if language == "" {
		language = "eng"
	}
	if len(language) > 64 || strings.ContainsAny(language, " ;|&\n\r\t") {
		return MediaResult{}, errors.New("invalid OCR language")
	}
	command := exec.CommandContext(ctx, "tesseract", input, "stdout", "-l", language)
	var output bytes.Buffer
	command.Stdout = &limitedWriter{writer: &output, limit: 8 << 20}
	command.Stderr = &limitedWriter{writer: &bytes.Buffer{}, limit: 1 << 20}
	if err := command.Run(); err != nil {
		return MediaResult{}, err
	}
	text := strings.TrimSpace(output.String())
	if text == "" {
		return MediaResult{}, errors.New("OCR returned no text")
	}
	path := filepath.Join(workspace, ".agent-media", "ocr-result.txt")
	if err := writeLimitedFile(path, []byte(text+"\n"), 8<<20); err != nil {
		return MediaResult{}, err
	}
	artifact, err := BuildArtifactManifest(workspace, "", "", filepath.Base(path), filepath.ToSlash(filepath.Join(".agent-media", filepath.Base(path))))
	if err != nil {
		return MediaResult{}, err
	}
	return MediaResult{Path: path, MediaType: "text/plain", Text: text, Artifact: artifact}, nil
}

type limitedWriter struct {
	writer *bytes.Buffer
	limit  int
}

func (w *limitedWriter) Write(data []byte) (int, error) {
	if len(data) > w.limit-w.writer.Len() {
		return 0, errors.New("process output exceeds limit")
	}
	return w.writer.Write(data)
}
