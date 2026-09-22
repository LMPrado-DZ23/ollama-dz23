package agent

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type BuilderKind string

const (
	BuilderWebsite   BuilderKind = "website"
	BuilderApp       BuilderKind = "app"
	BuilderGame      BuilderKind = "game"
	BuilderSlides    BuilderKind = "slides"
	BuilderDashboard BuilderKind = "dashboard"
)

type BuilderProject struct {
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	Kind          BuilderKind         `json:"kind"`
	Entry         string              `json:"entry"`
	Version       int                 `json:"version"`
	Status        string              `json:"status"`
	Root          string              `json:"root"`
	PreviewPath   string              `json:"preview_path,omitempty"`
	PublishedPath string              `json:"published_path,omitempty"`
	CreatedAt     time.Time           `json:"created_at"`
	UpdatedAt     time.Time           `json:"updated_at"`
	Components    []VisualComponent   `json:"components,omitempty"`
	UndoStack     [][]VisualComponent `json:"undo_stack,omitempty"`
	RedoStack     [][]VisualComponent `json:"redo_stack,omitempty"`
}

type VisualComponent struct {
	ID       string            `json:"id"`
	Type     string            `json:"type"`
	Props    map[string]string `json:"props,omitempty"`
	Style    map[string]string `json:"style,omitempty"`
	Bindings map[string]string `json:"bindings,omitempty"`
	Events   map[string]string `json:"events,omitempty"`
	Children []VisualComponent `json:"children,omitempty"`
	X        int               `json:"x,omitempty"`
	Y        int               `json:"y,omitempty"`
	Width    int               `json:"width,omitempty"`
	Height   int               `json:"height,omitempty"`
}

type BuilderSpec struct {
	Name       string            `json:"name"`
	Kind       BuilderKind       `json:"kind"`
	Entry      string            `json:"entry"`
	Files      map[string]string `json:"files"`
	Components []VisualComponent `json:"components,omitempty"`
}

type BuilderService struct {
	mu       sync.Mutex
	root     string
	projects map[string]BuilderProject
}

func NewBuilderService(root string) (*BuilderService, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("builder root is required")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	service := &BuilderService{root: root, projects: map[string]BuilderProject{}}
	data, err := os.ReadFile(filepath.Join(root, "projects.json"))
	if err == nil {
		if err := json.Unmarshal(data, &service.projects); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return service, nil
}

func (b *BuilderService) Create(ctx context.Context, spec BuilderSpec) (BuilderProject, error) {
	if err := ctx.Err(); err != nil {
		return BuilderProject{}, err
	}
	name := strings.TrimSpace(spec.Name)
	if name == "" || len(name) > 160 {
		return BuilderProject{}, errors.New("builder name is required and must be <= 160 characters")
	}
	if !validBuilderKind(spec.Kind) {
		return BuilderProject{}, errors.New("unsupported builder kind")
	}
	if len(spec.Files) == 0 && len(spec.Components) == 0 {
		spec.Files = templateFiles(spec.Kind, name)
	}
	if len(spec.Components) > 200 {
		return BuilderProject{}, errors.New("too many visual components")
	}
	if err := validateVisualComponents(spec.Components, 0); err != nil {
		return BuilderProject{}, err
	}
	if len(spec.Components) > 0 && len(spec.Files) == 0 {
		spec.Files = map[string]string{"index.html": renderVisualHTML(name, spec.Components), "visual.json": mustJSON(spec.Components)}
	}
	if len(spec.Files) > 200 {
		return BuilderProject{}, errors.New("too many builder files")
	}
	entry := strings.TrimSpace(spec.Entry)
	if entry == "" {
		entry = "index.html"
	}
	for path, contents := range spec.Files {
		if err := validateBuilderFile(path, contents); err != nil {
			return BuilderProject{}, err
		}
	}
	id := "bld_" + uuid.NewString()
	now := time.Now().UTC()
	projectRoot := filepath.Join(b.root, id)
	for relative, contents := range spec.Files {
		target := filepath.Join(projectRoot, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return BuilderProject{}, err
		}
		if err := os.WriteFile(target, []byte(contents), 0o600); err != nil {
			return BuilderProject{}, err
		}
	}
	project := BuilderProject{ID: id, Name: name, Kind: spec.Kind, Entry: filepath.ToSlash(entry), Version: 1, Status: "draft", Root: projectRoot, Components: spec.Components, CreatedAt: now, UpdatedAt: now}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.projects[id] = project
	if err := b.persistLocked(); err != nil {
		return BuilderProject{}, err
	}
	return project, nil
}

func (b *BuilderService) ApplyVisualComponents(ctx context.Context, id string, components []VisualComponent) (BuilderProject, error) {
	if err := ctx.Err(); err != nil {
		return BuilderProject{}, err
	}
	if len(components) > 200 {
		return BuilderProject{}, errors.New("too many visual components")
	}
	if err := validateVisualComponents(components, 0); err != nil {
		return BuilderProject{}, err
	}
	project, err := b.Get(id)
	if err != nil {
		return BuilderProject{}, err
	}
	indexPath := filepath.Join(project.Root, filepath.FromSlash(project.Entry))
	if err := os.WriteFile(indexPath, []byte(renderVisualHTML(project.Name, components)), 0o600); err != nil {
		return BuilderProject{}, err
	}
	if err := os.WriteFile(filepath.Join(project.Root, "visual.json"), []byte(mustJSON(components)), 0o600); err != nil {
		return BuilderProject{}, err
	}
	project.UndoStack = append(project.UndoStack, cloneComponents(project.Components))
	if len(project.UndoStack) > 50 {
		project.UndoStack = project.UndoStack[len(project.UndoStack)-50:]
	}
	project.RedoStack = nil
	project.Components = cloneComponents(components)
	project.Version++
	project.Status = "draft"
	project.UpdatedAt = time.Now().UTC()
	b.mu.Lock()
	b.projects[id] = project
	err = b.persistLocked()
	b.mu.Unlock()
	return project, err
}

func (b *BuilderService) Undo(ctx context.Context, id string) (BuilderProject, error) {
	if err := ctx.Err(); err != nil {
		return BuilderProject{}, err
	}
	project, err := b.Get(id)
	if err != nil {
		return BuilderProject{}, err
	}
	if len(project.UndoStack) == 0 {
		return project, errors.New("builder has no undo history")
	}
	previous := cloneComponents(project.UndoStack[len(project.UndoStack)-1])
	project.UndoStack = project.UndoStack[:len(project.UndoStack)-1]
	project.RedoStack = append(project.RedoStack, cloneComponents(project.Components))
	project.Components = previous
	if err := b.writeVisualProject(project); err != nil {
		return BuilderProject{}, err
	}
	project.Version++
	project.UpdatedAt = time.Now().UTC()
	b.mu.Lock()
	b.projects[id] = project
	err = b.persistLocked()
	b.mu.Unlock()
	return project, err
}

func (b *BuilderService) Redo(ctx context.Context, id string) (BuilderProject, error) {
	if err := ctx.Err(); err != nil {
		return BuilderProject{}, err
	}
	project, err := b.Get(id)
	if err != nil {
		return BuilderProject{}, err
	}
	if len(project.RedoStack) == 0 {
		return project, errors.New("builder has no redo history")
	}
	next := cloneComponents(project.RedoStack[len(project.RedoStack)-1])
	project.RedoStack = project.RedoStack[:len(project.RedoStack)-1]
	project.UndoStack = append(project.UndoStack, cloneComponents(project.Components))
	project.Components = next
	if err := b.writeVisualProject(project); err != nil {
		return BuilderProject{}, err
	}
	project.Version++
	project.UpdatedAt = time.Now().UTC()
	b.mu.Lock()
	b.projects[id] = project
	err = b.persistLocked()
	b.mu.Unlock()
	return project, err
}

func (b *BuilderService) writeVisualProject(project BuilderProject) error {
	indexPath := filepath.Join(project.Root, filepath.FromSlash(project.Entry))
	if err := os.WriteFile(indexPath, []byte(renderVisualHTML(project.Name, project.Components)), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(project.Root, "visual.json"), []byte(mustJSON(project.Components)), 0o600)
}

func (b *BuilderService) Get(id string) (BuilderProject, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	project, ok := b.projects[strings.TrimSpace(id)]
	if !ok {
		return BuilderProject{}, os.ErrNotExist
	}
	return project, nil
}

func (b *BuilderService) Preview(ctx context.Context, id string) (BuilderProject, ArtifactManifest, error) {
	if err := ctx.Err(); err != nil {
		return BuilderProject{}, ArtifactManifest{}, err
	}
	b.mu.Lock()
	project, ok := b.projects[strings.TrimSpace(id)]
	b.mu.Unlock()
	if !ok {
		return BuilderProject{}, ArtifactManifest{}, os.ErrNotExist
	}
	entryPath := filepath.Join(project.Root, filepath.FromSlash(project.Entry))
	if _, err := os.Stat(entryPath); err != nil {
		return BuilderProject{}, ArtifactManifest{}, err
	}
	project.PreviewPath = entryPath
	project.Status = "preview"
	project.UpdatedAt = time.Now().UTC()
	b.mu.Lock()
	b.projects[id] = project
	err := b.persistLocked()
	b.mu.Unlock()
	if err != nil {
		return BuilderProject{}, ArtifactManifest{}, err
	}
	artifact, err := BuildArtifactManifest(project.Root, "", "", filepath.Base(entryPath), project.Entry)
	if err != nil {
		return BuilderProject{}, ArtifactManifest{}, err
	}
	return project, artifact, nil
}

func (b *BuilderService) Export(ctx context.Context, id string) (BuilderProject, string, error) {
	if err := ctx.Err(); err != nil {
		return BuilderProject{}, "", err
	}
	project, err := b.Get(id)
	if err != nil {
		return BuilderProject{}, "", err
	}
	path := filepath.Join(b.root, id+".zip")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return BuilderProject{}, "", err
	}
	archive := zip.NewWriter(file)
	err = filepath.Walk(project.Root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(project.Root, path)
		if err != nil {
			return err
		}
		writer, err := archive.Create(filepath.ToSlash(relative))
		if err != nil {
			return err
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		_, err = io.Copy(writer, input)
		return err
	})
	if closeErr := archive.Close(); err == nil {
		err = closeErr
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return BuilderProject{}, "", err
	}
	return project, path, nil
}

func (b *BuilderService) PublishLocal(ctx context.Context, id string) (BuilderProject, string, error) {
	if err := ctx.Err(); err != nil {
		return BuilderProject{}, "", err
	}
	project, err := b.Get(id)
	if err != nil {
		return BuilderProject{}, "", err
	}
	published := filepath.Join(b.root, "published", id, fmt.Sprintf("v%d", project.Version))
	if err := os.MkdirAll(published, 0o700); err != nil {
		return BuilderProject{}, "", err
	}
	if err := copyFiles(project.Root, published); err != nil {
		return BuilderProject{}, "", err
	}
	project.PublishedPath = published
	project.Status = "published"
	project.UpdatedAt = time.Now().UTC()
	b.mu.Lock()
	b.projects[id] = project
	err = b.persistLocked()
	b.mu.Unlock()
	return project, published, err
}

func (b *BuilderService) List() []BuilderProject {
	b.mu.Lock()
	defer b.mu.Unlock()
	result := make([]BuilderProject, 0, len(b.projects))
	for _, project := range b.projects {
		result = append(result, project)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].UpdatedAt.Before(result[j].UpdatedAt) })
	return result
}

func (b *BuilderService) persistLocked() error {
	return writeJSONAtomic(filepath.Join(b.root, "projects.json"), b.projects)
}
func validBuilderKind(kind BuilderKind) bool {
	switch kind {
	case BuilderWebsite, BuilderApp, BuilderGame, BuilderSlides, BuilderDashboard:
		return true
	default:
		return false
	}
}
func validateBuilderFile(path, contents string) error {
	clean := filepath.ToSlash(filepath.Clean(path))
	if clean == "." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") || strings.Contains(clean, "../") {
		return errors.New("builder file path escapes project")
	}
	if len(contents) > 2<<20 {
		return errors.New("builder file exceeds 2 MiB")
	}
	return nil
}
func templateFiles(kind BuilderKind, name string) map[string]string {
	title := name
	switch kind {
	case BuilderGame:
		return map[string]string{"index.html": "<!doctype html><html><head><meta charset=\"utf-8\"><title>" + title + "</title></head><body><main id=\"game\"></main><script type=\"module\" src=\"game.ts\"></script></body></html>", "game.ts": "// Babylon.js game entrypoint. Add GameCanvas integration here.\nconst root = document.querySelector('#game'); if (root) root.textContent = 'Game preview ready';"}
	case BuilderSlides:
		return map[string]string{"index.html": "<!doctype html><html><head><meta charset=\"utf-8\"><title>" + title + " slides</title></head><body><section data-slide=\"1\"><h1>" + title + "</h1><p>Slide preview</p></section></body></html>"}
	case BuilderDashboard:
		return map[string]string{"index.html": "<!doctype html><html><head><meta charset=\"utf-8\"><title>" + title + " dashboard</title></head><body><main><h1>" + title + "</h1><section aria-label=\"Dashboard\"><article data-metric=\"one\">Metric ready</article></section></main></body></html>"}
	default:
		return map[string]string{"index.html": "<!doctype html><html><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"><title>" + title + "</title></head><body><main><h1>" + title + "</h1><p>Builder preview ready.</p></main></body></html>"}
	}
}
func mustJSON(value any) string {
	data, _ := json.MarshalIndent(value, "", "  ")
	return string(data)
}

func renderVisualHTML(name string, components []VisualComponent) string {
	data := mustJSON(components)
	return "<!doctype html><html><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"><title>" + htmlEscape(name) + "</title><style>body{font-family:system-ui;margin:0;padding:24px} [data-dz23-component]{border:1px dashed #bbb;padding:12px;margin:8px;border-radius:8px}</style></head><body><main id=\"dz23-canvas\"></main><script type=\"application/json\" id=\"dz23-visual\">" + scriptEscape(data) + "</script><script>const tree=JSON.parse(document.querySelector('#dz23-visual').textContent); const render=(nodes,parent)=>nodes.forEach(n=>{const el=document.createElement('section');el.dataset.dz23Component=n.type;el.textContent=(n.props&&n.props.text)||n.type;Object.assign(el.style,{position:n.x||n.y?'absolute':'static',left:(n.x||0)+'px',top:(n.y||0)+'px',width:n.width?(n.width+'px'):'auto',height:n.height?(n.height+'px'):'auto'});parent.appendChild(el);render(n.children||[],el)});render(tree,document.querySelector('#dz23-canvas'));</script></body></html>"
}

func validateVisualComponents(components []VisualComponent, depth int) error {
	if depth > 12 {
		return errors.New("visual component tree is too deep")
	}
	for _, component := range components {
		if strings.TrimSpace(component.ID) == "" || strings.TrimSpace(component.Type) == "" {
			return errors.New("visual components require id and type")
		}
		if len(component.Props)+len(component.Style)+len(component.Bindings)+len(component.Events) > 100 {
			return fmt.Errorf("visual component %q has too many properties", component.ID)
		}
		if err := validateVisualComponents(component.Children, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func cloneComponents(input []VisualComponent) []VisualComponent {
	if input == nil {
		return nil
	}
	output := make([]VisualComponent, len(input))
	for index, component := range input {
		output[index] = component
		output[index].Props = cloneStringMap(component.Props)
		output[index].Style = cloneStringMap(component.Style)
		output[index].Bindings = cloneStringMap(component.Bindings)
		output[index].Events = cloneStringMap(component.Events)
		output[index].Children = cloneComponents(component.Children)
	}
	return output
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func htmlEscape(value string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;").Replace(value)
}

func scriptEscape(value string) string {
	return strings.NewReplacer("&", "\\u0026", "<", "\\u003c", ">", "\\u003e").Replace(value)
}

func copyFiles(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		target := filepath.Join(dst, relative)
		if info.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
}
