package qconn

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// QNX open(2) flag values. These differ from Linux and are sent verbatim over
// the wire, so they MUST be the QNX (Neutrino) values. Source: openqnx
// trunk/lib/c/public/fcntl.h (octal).
const (
	O_RDONLY   = 0x0000
	O_WRONLY   = 0x0001
	O_RDWR     = 0x0002
	O_APPEND   = 0x0008 // 000010
	O_NONBLOCK = 0x0080 // 000200
	O_CREAT    = 0x0100 // 000400
	O_TRUNC    = 0x0200 // 001000
	O_EXCL     = 0x0400 // 002000
)

// QNX stat mode bits (octal-derived) used for mkdir / mode handling.
const (
	S_IFDIR   = 0x4000 // 040000
	S_IFREG   = 0x8000 // 0100000
	S_IFMT    = 0xF000 // 0170000
	modeRWAll = 0666
	modeDir   = 0777
)

// Buffer sizes. qconn (and qcl) are unreliable with large file reads; cap reads
// at 2 KiB and writes at 16 KiB per request.
const (
	maxReadChunk  = 2 * 1024
	maxWriteChunk = 16 * 1024
)

// openRe parses the file-open / stat response: o:<fd>:<mode>:<size>:"<path>".
var openRe = regexp.MustCompile(`^o:([0-9a-fA-F-]+):([0-9a-fA-F-]+):([0-9a-fA-F-]+):"(.+)"`)

// okRe matches the simple "o" success status (close/delete/chmod).
var okRe = regexp.MustCompile(`^o\s*$`)

// fileHandle is an open file descriptor on the target.
type fileHandle struct {
	fd   int64
	mode int64
	size int64
	path string
}

// openFile opens a target path via the file service.
func (c *Client) openFile(path string, flags, mode int) (*fileHandle, error) {
	cmd := fmt.Sprintf(`o:"%s":%x:%x`, path, flags, mode)
	resp, err := c.raw(cmd)
	if err != nil {
		return nil, err
	}
	m := openRe.FindStringSubmatch(firstLine(resp))
	if m == nil {
		return nil, fmt.Errorf("qconn open %q: %s", path, strings.TrimSpace(firstLine(resp)))
	}
	fd, _ := strconv.ParseInt(m[1], 16, 64)
	md, _ := strconv.ParseInt(m[2], 16, 64)
	sz, _ := strconv.ParseInt(m[3], 16, 64)
	return &fileHandle{fd: fd, mode: md, size: sz, path: m[4]}, nil
}

func (c *Client) closeFile(fd int64) error {
	resp, err := c.raw(fmt.Sprintf("c:%x", fd))
	if err != nil {
		return err
	}
	if !okRe.MatchString(firstLine(resp)) {
		return protoErr("close", resp)
	}
	return nil
}

// readAt reads up to n bytes from fd at off. The response is a status line
// "o:<numread>:..." followed by exactly numread raw bytes, then the prompt.
func (c *Client) readAt(fd, off int64, n int) ([]byte, error) {
	if n > maxReadChunk {
		n = maxReadChunk
	}
	if err := c.fc.writeLine(fmt.Sprintf("r:%x:%x:%x", fd, off, n)); err != nil {
		return nil, err
	}
	line, err := c.fc.readResponseLine()
	if err != nil {
		return nil, err
	}
	// The file service reports errors as "e:<message>"; consume the trailing
	// prompt to resync before returning.
	if strings.HasPrefix(line, "e:") {
		_, _ = c.fc.readUntilPrompt()
		return nil, fmt.Errorf("qconn read: %s", strings.TrimSpace(line[2:]))
	}
	parts := strings.SplitN(strings.TrimPrefix(line, "o:"), ":", 2)
	if len(parts) < 1 || !strings.HasPrefix(line, "o:") {
		_, _ = c.fc.readUntilPrompt()
		return nil, protoErr("read", line)
	}
	numread, err := strconv.ParseInt(parts[0], 16, 64)
	if err != nil {
		return nil, protoErr("read", line)
	}
	var data []byte
	if numread > 0 {
		data, err = c.fc.readN(int(numread))
		if err != nil {
			return nil, err
		}
	}
	// Consume the trailing prompt.
	if _, err := c.fc.readUntilPrompt(); err != nil {
		return data, err
	}
	return data, nil
}

// writeAt writes data to fd at off. The command line is followed by the raw
// payload, then a status line "o:<numwritten>:...", then the prompt.
func (c *Client) writeAt(fd, off int64, data []byte) (int, error) {
	if err := c.fc.writeLine(fmt.Sprintf("w:%x:%x:%x", fd, off, len(data))); err != nil {
		return 0, err
	}
	if err := c.fc.writeRaw(data); err != nil {
		return 0, err
	}
	line, err := c.fc.readResponseLine()
	if err != nil {
		return 0, err
	}
	if !strings.HasPrefix(line, "o:") {
		return 0, protoErr("write", line)
	}
	field := strings.TrimPrefix(line, "o:")
	if i := strings.IndexByte(field, ':'); i >= 0 {
		field = field[:i]
	}
	nw, _ := strconv.ParseInt(field, 16, 64)
	if _, err := c.fc.readUntilPrompt(); err != nil {
		return int(nw), err
	}
	return int(nw), nil
}

// Stat opens then closes a path to retrieve its mode and size.
func (c *Client) Stat(ctx context.Context, path string) (mode, size int64, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err = c.ensure(ServiceFile); err != nil {
		return
	}
	h, err := c.openFile(path, O_RDONLY, 0)
	if err != nil {
		return 0, 0, err
	}
	defer c.closeFile(h.fd)
	return h.mode, h.size, nil
}

// ReadFile reads an entire target file (chunked).
func (c *Client) ReadFile(ctx context.Context, path string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensure(ServiceFile); err != nil {
		return nil, err
	}
	h, err := c.openFile(path, O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer c.closeFile(h.fd)
	var out []byte
	off := int64(0)
	for {
		chunk, err := c.readAt(h.fd, off, maxReadChunk)
		if err != nil {
			return out, err
		}
		if len(chunk) == 0 {
			break
		}
		out = append(out, chunk...)
		off += int64(len(chunk))
		if h.size > 0 && off >= h.size {
			break
		}
	}
	return out, nil
}

// ReadFileRange reads up to n bytes of a target file starting at off. Used for
// raw memory reads against /proc/<pid>/as where the file is huge/sparse.
func (c *Client) ReadFileRange(ctx context.Context, path string, off int64, n int) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensure(ServiceFile); err != nil {
		return nil, err
	}
	h, err := c.openFile(path, O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer c.closeFile(h.fd)
	var out []byte
	for len(out) < n {
		want := n - len(out)
		if want > maxReadChunk {
			want = maxReadChunk
		}
		chunk, err := c.readAt(h.fd, off+int64(len(out)), want)
		if err != nil {
			return out, err
		}
		if len(chunk) == 0 {
			break
		}
		out = append(out, chunk...)
	}
	return out, nil
}

// WriteFile creates (or truncates) a target file and writes data (chunked).
func (c *Client) WriteFile(ctx context.Context, path string, data []byte, mode int) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if mode == 0 {
		mode = modeRWAll
	}
	if err := c.ensure(ServiceFile); err != nil {
		return 0, err
	}
	h, err := c.openFile(path, O_CREAT|O_TRUNC|O_WRONLY, mode)
	if err != nil {
		return 0, err
	}
	defer c.closeFile(h.fd)
	total := 0
	for total < len(data) {
		end := total + maxWriteChunk
		if end > len(data) {
			end = len(data)
		}
		nw, err := c.writeAt(h.fd, int64(total), data[total:end])
		if err != nil {
			return total, err
		}
		if nw <= 0 {
			break
		}
		total += nw
	}
	return total, nil
}

// WriteFileAt writes data at a specific offset of an existing file (O_RDWR),
// used for raw memory writes against /proc/<pid>/as.
func (c *Client) WriteFileAt(ctx context.Context, path string, off int64, data []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensure(ServiceFile); err != nil {
		return 0, err
	}
	h, err := c.openFile(path, O_RDWR, 0)
	if err != nil {
		return 0, err
	}
	defer c.closeFile(h.fd)
	total := 0
	for total < len(data) {
		end := total + maxWriteChunk
		if end > len(data) {
			end = len(data)
		}
		nw, err := c.writeAt(h.fd, off+int64(total), data[total:end])
		if err != nil {
			return total, err
		}
		if nw <= 0 {
			break
		}
		total += nw
	}
	return total, nil
}

// Delete removes a target file or directory.
func (c *Client) Delete(ctx context.Context, path string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensure(ServiceFile); err != nil {
		return err
	}
	resp, err := c.raw(fmt.Sprintf(`d:"%s":1`, path))
	if err != nil {
		return err
	}
	if !okRe.MatchString(firstLine(resp)) {
		return fmt.Errorf("qconn delete %q: %s", path, strings.TrimSpace(firstLine(resp)))
	}
	return nil
}

// Mkdir creates a directory on the target.
func (c *Client) Mkdir(ctx context.Context, path string, mode int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if mode == 0 {
		mode = modeDir
	}
	if err := c.ensure(ServiceFile); err != nil {
		return err
	}
	h, err := c.openFile(path, O_CREAT|O_WRONLY, S_IFDIR|mode)
	if err != nil {
		return err
	}
	return c.closeFile(h.fd)
}

// Chmod changes a target path's permission bits.
func (c *Client) Chmod(ctx context.Context, path string, mode int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensure(ServiceFile); err != nil {
		return err
	}
	h, err := c.openFile(path, O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer c.closeFile(h.fd)
	resp, err := c.raw(fmt.Sprintf("a:%x:%x", h.fd, mode))
	if err != nil {
		return err
	}
	if !okRe.MatchString(firstLine(resp)) {
		return protoErr("chmod", resp)
	}
	return nil
}
