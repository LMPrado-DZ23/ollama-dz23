//go:build darwin

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
	return runDesktop(ctx, "screencapture", "-x", path)
}
func desktopMouseClick(ctx context.Context, x, y int) error {
	return runDesktop(ctx, "cliclick", "c:"+strconv.Itoa(x)+","+strconv.Itoa(y))
}
func desktopKeyboardType(ctx context.Context, text string) error {
	return runDesktopWithInput(ctx, text, "cliclick", "t:"+text)
}
func desktopClipboardGet(ctx context.Context) (string, error) {
	return runDesktopOutput(ctx, "pbpaste")
}
func desktopClipboardSet(ctx context.Context, text string) error {
	return runDesktopWithInput(ctx, text, "pbcopy")
}
func desktopProcessList(ctx context.Context) (string, error) {
	return runDesktopOutput(ctx, "ps", "-axo", "pid=,comm=,args=")
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
