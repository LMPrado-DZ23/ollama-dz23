//go:build windows

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
	script := `Add-Type -AssemblyName System.Windows.Forms; Add-Type -AssemblyName System.Drawing; $screen=[System.Windows.Forms.Screen]::PrimaryScreen.Bounds; $bitmap=New-Object System.Drawing.Bitmap($screen.Width,$screen.Height); $graphics=[System.Drawing.Graphics]::FromImage($bitmap); $graphics.CopyFromScreen($screen.Location,[System.Drawing.Point]::Empty,$screen.Size); $bitmap.Save($env:DZ23_PATH,[System.Drawing.Imaging.ImageFormat]::Png); $graphics.Dispose(); $bitmap.Dispose()`
	return runPowerShell(ctx, script, map[string]string{"DZ23_PATH": path})
}
func desktopMouseClick(ctx context.Context, x, y int) error {
	script := `Add-Type @"` + "\nusing System; using System.Runtime.InteropServices; public static class DZ23Mouse { [DllImport(\"user32.dll\")] public static extern bool SetCursorPos(int X, int Y); [DllImport(\"user32.dll\")] public static extern void mouse_event(uint flags, uint dx, uint dy, uint data, UIntPtr extra); }" + "\n" + `"@; [DZ23Mouse]::SetCursorPos(` + strconv.Itoa(x) + `,` + strconv.Itoa(y) + `); [DZ23Mouse]::mouse_event(2,0,0,0,[UIntPtr]::Zero); [DZ23Mouse]::mouse_event(4,0,0,0,[UIntPtr]::Zero)`
	return runPowerShell(ctx, script, nil)
}
func desktopKeyboardType(ctx context.Context, text string) error {
	return runPowerShell(ctx, `Add-Type -AssemblyName System.Windows.Forms; [System.Windows.Forms.SendKeys]::SendWait($env:DZ23_INPUT)`, map[string]string{"DZ23_INPUT": text})
}
func desktopClipboardGet(ctx context.Context) (string, error) {
	return runPowerShellOutput(ctx, `Get-Clipboard -Raw`, nil)
}
func desktopClipboardSet(ctx context.Context, text string) error {
	return runPowerShell(ctx, `Set-Clipboard -Value $env:DZ23_INPUT`, map[string]string{"DZ23_INPUT": text})
}
func desktopProcessList(ctx context.Context) (string, error) {
	return runPowerShellOutput(ctx, `Get-Process | Select-Object Id,ProcessName | ConvertTo-Json -Compress`, nil)
}
func desktopProcessTerminate(ctx context.Context, pid int) error {
	return runPowerShell(ctx, `Stop-Process -Id `+strconv.Itoa(pid)+` -Force:$false`, nil)
}

func runDesktop(ctx context.Context, name string, args ...string) error {
	_, err := runDesktopOutput(ctx, name, args...)
	return err
}
func runDesktopOutput(ctx context.Context, name string, args ...string) (string, error) {
	deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	command := exec.CommandContext(deadline, name, args...)
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
	command.Stdin = strings.NewReader(input)
	return command.Run()
}
func runPowerShell(ctx context.Context, script string, variables map[string]string) error {
	_, err := runPowerShellOutput(ctx, script, variables)
	return err
}
func runPowerShellOutput(ctx context.Context, script string, variables map[string]string) (string, error) {
	deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	executable := "powershell.exe"
	if _, err := exec.LookPath(executable); err != nil {
		executable = "pwsh"
	}
	command := exec.CommandContext(deadline, executable, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	command.Env = os.Environ()
	for key, value := range variables {
		command.Env = append(command.Env, key+"="+value)
	}
	var stdout, stderr bytes.Buffer
	command.Stdout = &limitedBuffer{Buffer: &stdout, Limit: 128 << 10}
	command.Stderr = &limitedBuffer{Buffer: &stderr, Limit: 32 << 10}
	if err := command.Run(); err != nil {
		return stdout.String(), fmt.Errorf("powershell desktop action: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
