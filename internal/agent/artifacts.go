package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func BuildArtifactManifest(workspace, missionID, stepID, name, relativePath string) (ArtifactManifest, error) {
	if strings.TrimSpace(workspace) == "" {
		return ArtifactManifest{}, errors.New("workspace is required")
	}
	root, err := filepath.Abs(workspace)
	if err != nil {
		return ArtifactManifest{}, err
	}
	candidate, err := filepath.Abs(filepath.Join(root, relativePath))
	if err != nil {
		return ArtifactManifest{}, err
	}
	if !isWithin(root, candidate) {
		return ArtifactManifest{}, errors.New("artifact path escapes workspace")
	}
	info, err := os.Stat(candidate)
	if err != nil {
		return ArtifactManifest{}, err
	}
	if info.IsDir() {
		return ArtifactManifest{}, errors.New("artifact path is a directory")
	}
	file, err := os.Open(candidate)
	if err != nil {
		return ArtifactManifest{}, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return ArtifactManifest{}, err
	}
	mediaType := mime.TypeByExtension(filepath.Ext(candidate))
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	if strings.TrimSpace(name) == "" {
		name = filepath.Base(candidate)
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return ArtifactManifest{}, err
	}
	return ArtifactManifest{
		ID:        fmt.Sprintf("art_%d", time.Now().UnixNano()),
		MissionID: missionID,
		StepID:    stepID,
		Name:      name,
		Path:      filepath.ToSlash(relative),
		MediaType: mediaType,
		Size:      info.Size(),
		SHA256:    hex.EncodeToString(hash.Sum(nil)),
		CreatedAt: time.Now().UTC(),
	}, nil
}

func isWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
