package qconn

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// okPidRe matches the launcher's "OK <pid>" acknowledgement.
var okPidRe = regexp.MustCompile(`^OK\s+(\d+)`)

// ExecResult is the outcome of a captured command execution.
type ExecResult struct {
	PID    int    `json:"pid"`
	Output string `json:"output"`
}

// Exec runs a shell command on the target via the launcher service and returns
// its combined stdout/stderr. The command is executed as:
//
//	/bin/sh -c "<command>"
//
// using the launcher's symbolic "run" flag (load + start immediately), which
// streams the child's output back over the connection until it exits. Running
// through /bin/sh gives PATH resolution, redirection and globbing, matching how
// the nmap qconn-exec script and the Momentics IDE drive the launcher.
//
// NOTE: qconn performs NO authentication and runs commands as root. Treat this
// as a privileged operation.
func (c *Client) Exec(ctx context.Context, command string) (*ExecResult, error) {
	// The launcher "run" flag is one-shot: it streams the child's stdout/stderr
	// and never returns to a prompt, leaving the connection unusable for further
	// commands. So exec runs on its own dedicated, short-lived connection rather
	// than the pooled one.
	d := net.Dialer{Timeout: c.cfg.Timeout}
	conn, err := d.DialContext(ctx, "tcp", c.Target())
	if err != nil {
		return nil, fmt.Errorf("qconn exec dial: %w", err)
	}
	defer conn.Close()
	fc := newFrameConn(conn, c.log, c.cfg.Timeout)

	if err := fc.handshake(); err != nil {
		return nil, fmt.Errorf("qconn exec handshake: %w", err)
	}
	if err := fc.writeLine("service launcher"); err != nil {
		return nil, err
	}
	resp, err := fc.readUntilPrompt()
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(firstLine(resp), "OK") {
		return nil, protoErr("service launcher", resp)
	}

	cmd := fmt.Sprintf(`start/flags run /bin/sh /bin/sh -c "%s"`, escapeArg(command))
	if err := fc.writeLine(cmd); err != nil {
		return nil, err
	}
	// "OK <pid>\r\n" + child output, with no trailing prompt; read until idle.
	out := string(fc.readUntilIdle(1500 * time.Millisecond))
	out = stripLeadingNoise(strings.TrimLeft(out, " \r\n"))

	res := &ExecResult{}
	if i := strings.IndexByte(out, '\n'); i >= 0 {
		head := strings.TrimRight(out[:i], "\r")
		if m := okPidRe.FindStringSubmatch(head); m != nil {
			res.PID, _ = strconv.Atoi(m[1])
			res.Output = out[i+1:]
		} else {
			res.Output = out // no OK line; return whatever came back
		}
	} else if m := okPidRe.FindStringSubmatch(strings.TrimSpace(out)); m != nil {
		res.PID, _ = strconv.Atoi(m[1])
	} else {
		res.Output = out
	}
	c.log.Debug("exec", "command", command, "pid", res.PID, "bytes", len(res.Output))
	return res, nil
}

// Run launches a program in the background on the target and returns
// immediately. It is implemented by backgrounding through the shell so the call
// does not block on a long-lived process; the reported PID is the shell's.
func (c *Client) Run(ctx context.Context, program string, args ...string) (int, error) {
	full := program
	if len(args) > 0 {
		full += " " + strings.Join(args, " ")
	}
	// Background and print the child PID, then exit the shell promptly.
	r, err := c.Exec(ctx, full+" >/dev/null 2>&1 & echo $!")
	if err != nil {
		return 0, err
	}
	if pid, err := strconv.Atoi(strings.TrimSpace(r.Output)); err == nil {
		return pid, nil
	}
	return r.PID, nil
}

// escapeArg escapes a command for embedding inside the launcher's double-quoted
// argument. Backslashes and double-quotes are escaped; newlines (which would
// terminate the command line) are collapsed to spaces.
func escapeArg(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return s
}
