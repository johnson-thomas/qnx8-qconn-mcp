package qconn

import (
	"context"
	"fmt"
	"strings"
)

// Common QNX signal numbers (POSIX-aligned) for convenience/validation.
const (
	SIGHUP  = 1
	SIGINT  = 2
	SIGQUIT = 3
	SIGKILL = 9
	SIGTERM = 15
	SIGSTOP = 17 // NOTE: QNX uses 17 for SIGSTOP, 18 for SIGCONT (not Linux values)
	SIGCONT = 19
)

// Signal sends a signal to a process via the cntl service: "kill <pid> <sig>".
// The daemon answers "ok" on success.
func (c *Client) Signal(ctx context.Context, pid, sig int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensure(ServiceCntl); err != nil {
		return err
	}
	resp, err := c.raw(fmt.Sprintf("kill %d %d", pid, sig))
	if err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(firstLine(resp)), "ok") {
		return protoErr("kill", resp)
	}
	c.log.Debug("signal sent", "pid", pid, "sig", sig)
	return nil
}

// Kill is a convenience wrapper that sends SIGKILL.
func (c *Client) Kill(ctx context.Context, pid int) error {
	return c.Signal(ctx, pid, SIGKILL)
}
