//go:build linux

package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func desktopScreenshot(ctx context.Context, path string) error {
	return runDesktop(ctx, "import", "-window", "root", path)
}
func desktopMouseClick(ctx context.Context, x, y int) error {
	return runDesktop(ctx, "xdotool", "mousemove", "--sync", strconv.Itoa(x), strconv.Itoa(y), "click", "1")
}
func desktopKeyboardType(ctx context.Context, text string) error {
	return runDesktop(ctx, "xdotool", "type", "--clearmodifiers", "--delay", "1", text)
}
func desktopClipboardGet(ctx context.Context) (string, error) {
	return runDesktopOutput(ctx, "xclip", "-selection", "clipboard", "-o")
}
func desktopClipboardSet(ctx context.Context, text string) error {
	return runDesktopWithInput(ctx, text, "xclip", "-selection", "clipboard")
}
func desktopProcessList(ctx context.Context) (string, error) {
	return runDesktopOutput(ctx, "ps", "-eo", "pid=,comm=,args=")
}
func desktopProcessTerminate(ctx context.Context, pid int) error {
	return runDesktop(ctx, "kill", "-TERM", strconv.Itoa(pid))
}

func runDesktop(ctx context.Context, name string, args ...string) error {
	_, err := runDesktopOutput(ctx, name, args...)
	return err
}

func runDesktopOutput(ctx context.Context, name string, args ...string) (string, error) {
	deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	command := exec.CommandContext(deadline, name, args...)
	command.Env = append(os.Environ(), "LC_ALL=C")
	var stdout, stderr bytes.Buffer
	command.Stdout = &limitedBuffer{Buffer: &stdout, Limit: 128 << 10}
	command.Stderr = &limitedBuffer{Buffer: &stderr, Limit: 32 << 10}
	if err := command.Run(); err != nil {
		return stdout.String(), fmt.Errorf("desktop %s: %w: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func runDesktopWithInput(ctx context.Context, input, name string, args ...string) error {
	deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	command := exec.CommandContext(deadline, name, args...)
	command.Env = append(os.Environ(), "LC_ALL=C")
	command.Stdin = strings.NewReader(input)
	return command.Run()
}
