//go:build windows

package oracle

import (
	"context"
	"os"
	"os/exec"
	"syscall"
)

// shellCommand runs line with cmd.exe.
//
// Go's normal argument escaping mangles quotes for cmd.exe, so the raw
// command line is set directly. /S tells cmd to strip only the outer pair
// of quotes we add, leaving the user's own quotes intact.
func shellCommand(ctx context.Context, line string) *exec.Cmd {
	shell := os.Getenv("ComSpec")
	if shell == "" {
		shell = "cmd.exe"
	}
	cmd := exec.CommandContext(ctx, shell)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine: `cmd /S /C "` + line + `"`,
	}
	return cmd
}
