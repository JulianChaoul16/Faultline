//go:build !windows

package oracle

import (
	"context"
	"os/exec"
)

// shellCommand runs line with /bin/sh, so pipes and quoting work as users
// expect from their terminal.
func shellCommand(ctx context.Context, line string) *exec.Cmd {
	return exec.CommandContext(ctx, "/bin/sh", "-c", line)
}
