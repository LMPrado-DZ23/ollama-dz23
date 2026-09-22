package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

type desktopCompanionTool struct{}

func (desktopCompanionTool) Descriptor() ToolDescriptor {
	return ToolDescriptor{Name: "desktop.companion", Version: "1", Description: "Operações controladas de tela, mouse, teclado, clipboard e processos no Desktop local", Risk: RiskExternalSideEffect, RequiresApproval: true, Scopes: []string{"desktop:screen", "desktop:input", "desktop:clipboard", "desktop:process"}}
}

func (desktopCompanionTool) Execute(ctx context.Context, toolContext ToolContext, input map[string]any) (ToolResult, error) {
	action := strings.TrimSpace(stringInput(input, "action", ""))
	if action == "" {
		return ToolResult{}, errors.New("desktop action is required")
	}
	switch action {
	case "screenshot":
		relativePath := stringInput(input, "save_path", "desktop-screenshot.png")
		path, err := safeWorkspacePath(toolContext.Workspace, relativePath)
		if err != nil {
			return ToolResult{}, err
		}
		if err := desktopScreenshot(ctx, path); err != nil {
			return ToolResult{}, err
		}
		manifest, err := BuildArtifactManifest(toolContext.Workspace, toolContext.MissionID, toolContext.StepID, "desktop-screenshot", relativePath)
		if err != nil {
			return ToolResult{}, err
		}
		return ToolResult{Value: map[string]any{"action": action, "path": path}, Artifacts: []ArtifactManifest{manifest}}, nil
	case "mouse_click":
		x := intInput(input, "x", -1)
		if x < 0 || x > 10000 {
			return ToolResult{}, errors.New("mouse x must be between 0 and 10000")
		}
		y := intInput(input, "y", -1)
		if y < 0 || y > 10000 {
			return ToolResult{}, errors.New("mouse y must be between 0 and 10000")
		}
		if err := desktopMouseClick(ctx, x, y); err != nil {
			return ToolResult{}, err
		}
		return ToolResult{Value: map[string]any{"action": action, "x": x, "y": y}}, nil
	case "keyboard_type":
		text := stringInput(input, "text", "")
		if len(text) > 4096 || strings.ContainsRune(text, '\x00') {
			return ToolResult{}, errors.New("keyboard text is empty/too long or contains NUL")
		}
		if err := desktopKeyboardType(ctx, text); err != nil {
			return ToolResult{}, err
		}
		return ToolResult{Value: map[string]any{"action": action, "bytes": len(text)}}, nil
	case "clipboard_get":
		output, err := desktopClipboardGet(ctx)
		if err != nil {
			return ToolResult{}, err
		}
		return ToolResult{Value: map[string]any{"action": action, "text": limitString(output, 64<<10)}}, nil
	case "clipboard_set":
		text := stringInput(input, "text", "")
		if len(text) > 64<<10 || strings.ContainsRune(text, '\x00') {
			return ToolResult{}, errors.New("clipboard text is empty/too long or contains NUL")
		}
		if err := desktopClipboardSet(ctx, text); err != nil {
			return ToolResult{}, err
		}
		return ToolResult{Value: map[string]any{"action": action, "bytes": len(text)}}, nil
	case "process_list":
		output, err := desktopProcessList(ctx)
		if err != nil {
			return ToolResult{}, err
		}
		return ToolResult{Value: map[string]any{"action": action, "processes": strings.FieldsFunc(limitString(output, 128<<10), func(r rune) bool { return r == '\n' })}}, nil
	case "process_terminate":
		pid := intInput(input, "pid", -1)
		if pid <= 1 || pid == os.Getpid() {
			return ToolResult{}, errors.New("refusing to terminate invalid or current process")
		}
		if err := desktopProcessTerminate(ctx, pid); err != nil {
			return ToolResult{}, err
		}
		return ToolResult{Value: map[string]any{"action": action, "pid": pid, "signal": "TERM"}}, nil
	default:
		return ToolResult{}, fmt.Errorf("unsupported desktop action %q", action)
	}
}

func limitString(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
