package qconn

import (
	"context"
	"strconv"
	"strings"
)

// Thread is a single thread of a process as reported by pidin.
type Thread struct {
	Tid      int    `json:"tid"`
	Priority string `json:"priority"` // e.g. "10r" (priority + scheduling policy letter)
	State    string `json:"state"`    // e.g. RECEIVE, READY, RUNNING, NANOSLEEP
	Blocked  string `json:"blocked,omitempty"`
}

// Process aggregates a target process and its threads.
type Process struct {
	PID        int      `json:"pid"`
	Name       string   `json:"name"`
	NumThreads int      `json:"num_threads"`
	Threads    []Thread `json:"threads,omitempty"`
}

// MemUsage is per-process memory accounting (from `pidin mem`).
type MemUsage struct {
	PID   int    `json:"pid"`
	Name  string `json:"name"`
	Code  string `json:"code,omitempty"`
	Data  string `json:"data,omitempty"`
	Stack string `json:"stack,omitempty"`
}

// ListProcesses returns all processes (grouped from pidin's per-thread listing).
//
// Parsing is deliberately tolerant: pidin's exact columns vary slightly across
// QNX releases, so callers that need ground truth can use ExecRaw / the Resources
// helper, which also returns the raw text.
func (c *Client) ListProcesses(ctx context.Context) ([]Process, string, error) {
	r, err := c.Exec(ctx, "pidin -F \"%a %b %N %p %J %B\"")
	if err != nil {
		return nil, "", err
	}
	procs := parsePidinThreads(r.Output)
	if len(procs) == 0 {
		// Fall back to the default pidin format if the -F format was unsupported.
		if r2, err2 := c.Exec(ctx, "pidin"); err2 == nil {
			procs = parsePidinThreads(r2.Output)
			return procs, r2.Output, nil
		}
	}
	return procs, r.Output, nil
}

// ProcessInfo returns a single process with its threads.
func (c *Client) ProcessInfo(ctx context.Context, pid int) (*Process, string, error) {
	r, err := c.Exec(ctx, "pidin -p "+strconv.Itoa(pid))
	if err != nil {
		return nil, "", err
	}
	procs := parsePidinThreads(r.Output)
	for i := range procs {
		if procs[i].PID == pid {
			return &procs[i], r.Output, nil
		}
	}
	if len(procs) == 1 {
		return &procs[0], r.Output, nil
	}
	return &Process{PID: pid}, r.Output, nil
}

// ProcessMemory returns per-process memory usage. With pid<=0 it returns all.
func (c *Client) ProcessMemory(ctx context.Context, pid int) ([]MemUsage, string, error) {
	cmd := "pidin mem"
	if pid > 0 {
		cmd = "pidin -p " + strconv.Itoa(pid) + " mem"
	}
	r, err := c.Exec(ctx, cmd)
	if err != nil {
		return nil, "", err
	}
	return parsePidinMem(r.Output), r.Output, nil
}

// SystemMemory returns the target's system-wide memory/cpu summary (pidin info).
func (c *Client) SystemMemory(ctx context.Context) (map[string]string, string, error) {
	r, err := c.Exec(ctx, "pidin info")
	if err != nil {
		return nil, "", err
	}
	return parseKeyColon(r.Output), r.Output, nil
}

// Resources returns a pidin sub-listing for a process (or system-wide). Valid
// kinds include: fds, signals, timers, channels, mem, irqs, env, args, sched,
// names, pmem, on. The structured lines plus the raw text are returned.
func (c *Client) Resources(ctx context.Context, pid int, kind string) ([]string, string, error) {
	cmd := "pidin"
	if pid > 0 {
		cmd += " -p " + strconv.Itoa(pid)
	}
	if kind != "" {
		cmd += " " + kind
	}
	r, err := c.Exec(ctx, cmd)
	if err != nil {
		return nil, "", err
	}
	return splitLines(r.Output), r.Output, nil
}

// --- parsers -------------------------------------------------------------

// parsePidinThreads parses pidin per-thread output and groups it by pid.
// Expected leading columns: pid tid name prio STATE [Blocked...].
func parsePidinThreads(out string) []Process {
	byPid := map[int]*Process{}
	var order []int
	for _, ln := range splitLines(out) {
		f := strings.Fields(ln)
		if len(f) < 5 {
			continue
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil {
			continue // header or non-data line
		}
		tid, err := strconv.Atoi(f[1])
		if err != nil {
			continue
		}
		name := f[2]
		prio := f[3]
		state := f[4]
		blocked := ""
		if len(f) > 5 {
			blocked = strings.Join(f[5:], " ")
		}
		p, ok := byPid[pid]
		if !ok {
			p = &Process{PID: pid, Name: name}
			byPid[pid] = p
			order = append(order, pid)
		}
		p.Threads = append(p.Threads, Thread{Tid: tid, Priority: prio, State: state, Blocked: blocked})
		p.NumThreads = len(p.Threads)
	}
	out2 := make([]Process, 0, len(order))
	for _, pid := range order {
		out2 = append(out2, *byPid[pid])
	}
	return out2
}

// parsePidinMem parses `pidin mem`. Columns vary, but the first data fields are
// pid tid name ... with code/data/stack near the end.
func parsePidinMem(out string) []MemUsage {
	seen := map[int]bool{}
	var res []MemUsage
	for _, ln := range splitLines(out) {
		f := strings.Fields(ln)
		if len(f) < 6 {
			continue
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		if seen[pid] {
			continue
		}
		seen[pid] = true
		m := MemUsage{PID: pid, Name: f[2]}
		// Best-effort: take the last three space-separated fields as code/data/stack.
		n := len(f)
		if n >= 3 {
			m.Code = f[n-3]
			m.Data = f[n-2]
			m.Stack = f[n-1]
		}
		res = append(res, m)
	}
	return res
}

// parseKeyColon parses "Key: value" lines into a map.
func parseKeyColon(out string) map[string]string {
	m := map[string]string{}
	for _, ln := range splitLines(out) {
		if k, v, ok := strings.Cut(ln, ":"); ok {
			m[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return m
}
