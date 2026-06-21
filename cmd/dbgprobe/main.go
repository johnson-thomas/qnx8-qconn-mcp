// Command dbgprobe validates the QNX DSMSG debug protocol against a real pdebug
// listening on a TCP port.
//
//	dbgprobe -addr 192.168.2.10:8001 -pid 12345 -addr-rd 0x...
package main

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/johnson-thomas/qnx8-qconn-mcp/internal/obs"
	"github.com/johnson-thomas/qnx8-qconn-mcp/internal/qnxdbg"
)

func main() {
	addr := flag.String("addr", "192.168.2.10:8001", "pdebug host:port")
	pid := flag.Int("pid", 0, "pid to attach to")
	rd := flag.String("rd", "0", "hex virtual address to read 32 bytes from")
	trace := flag.Bool("trace", false, "wire trace")
	execTest := flag.Bool("exec", false, "test breakpoint/continue/stop")
	brk := flag.String("brk", "0", "hex address to set a breakpoint at, then continue until it is hit")
	launch := flag.String("launch", "", "TARGET path of a program to launch under the debugger (DSMSG Load)")
	mainAddr := flag.String("main", "0", "hex address of main; after launch, break there and continue to it")
	flag.Parse()

	if *launch != "" {
		ctx := context.Background()
		lvl := "info"
		if *trace {
			lvl = "debug"
		}
		c, err := qnxdbg.Connect(ctx, *addr, obs.Setup(obs.Options{Level: lvl, Trace: *trace}), 10*time.Second)
		if err != nil {
			fmt.Println("connect:", err)
			os.Exit(1)
		}
		defer c.Close()
		fmt.Println("=== handshake OK ===")
		res, err := c.Launch(ctx, *launch, nil, nil)
		if err != nil {
			fmt.Println("launch:", err)
			os.Exit(1)
		}
		fmt.Printf("LAUNCHED %s -> pid=%d tid=%d (stopped at entry)\n", *launch, res.PID, res.TID)
		if err := c.Select(ctx, res.PID, res.TID); err != nil {
			fmt.Println("select:", err)
		}
		readPC := func() uint64 {
			if b, err := c.ReadRegisters(ctx, 0x100, 8); err == nil && len(b) == 8 {
				return binary.LittleEndian.Uint64(b)
			}
			return 0
		}
		fmt.Printf("entry PC = %#x\n", readPC())
		if m, _ := strconv.ParseUint(trimHex(*mainAddr), 16, 64); m != 0 {
			if err := c.SetBreakpoint(ctx, m, 0); err != nil {
				fmt.Println("break main:", err)
			} else {
				st, err := c.Continue(ctx)
				if err != nil {
					fmt.Println("continue:", err)
				} else {
					fmt.Printf("STOPPED at main: code=%#x pc=%#x (expected %#x)\n", st.Code, readPC(), m)
				}
			}
		}
		_ = c.Kill(ctx, res.PID)
		return
	}

	lvl := "info"
	if *trace {
		lvl = "debug"
	}
	log := obs.Setup(obs.Options{Level: lvl, Trace: *trace})
	ctx := context.Background()

	c, err := qnxdbg.Connect(ctx, *addr, log, 8*time.Second)
	if err != nil {
		fmt.Println("connect:", err)
		os.Exit(1)
	}
	defer c.Close()
	fmt.Println("=== handshake OK ===")

	if *pid == 0 {
		return
	}
	if err := c.Attach(ctx, *pid); err != nil {
		fmt.Println("attach:", err)
		return
	}
	fmt.Printf("attached to pid %d\n", *pid)

	if err := c.Select(ctx, *pid, 1); err != nil {
		fmt.Println("select:", err)
	} else {
		fmt.Println("selected tid 1")
	}

	if regs, err := c.ReadRegisters(ctx, 0, 0x230); err != nil {
		fmt.Println("regrd:", err)
	} else {
		fmt.Printf("registers (%d bytes): %s\n", len(regs), hex.EncodeToString(regs))
	}

	rdAddr, _ := strconv.ParseUint(trimHex(*rd), 16, 64)
	if rdAddr != 0 {
		if mem, err := c.ReadMemory(ctx, rdAddr, 32); err != nil {
			fmt.Println("memrd:", err)
		} else {
			fmt.Printf("mem @%#x (%d): %s\n", rdAddr, len(mem), hex.EncodeToString(mem))
		}
	}

	if *execTest {
		// Read the current PC (aarch64 QNX: ELR at register-area offset 0x100)
		// to set a breakpoint at known-valid code.
		pcAddr := rdAddr
		if pcb, err := c.ReadRegisters(ctx, 0x100, 8); err == nil && len(pcb) == 8 {
			pc := uint64(pcb[0]) | uint64(pcb[1])<<8 | uint64(pcb[2])<<16 | uint64(pcb[3])<<24 |
				uint64(pcb[4])<<32 | uint64(pcb[5])<<40 | uint64(pcb[6])<<48 | uint64(pcb[7])<<56
			fmt.Printf("PC (off 0x100) = %#x\n", pc)
			if pc != 0 {
				pcAddr = pc
			}
		}
		if err := c.SetBreakpoint(ctx, pcAddr, 4); err != nil {
			fmt.Printf("brk set @%#x: %v\n", pcAddr, err)
		} else {
			fmt.Printf("breakpoint set @%#x OK\n", pcAddr)
			_ = c.ClearBreakpoint(ctx, pcAddr)
		}
	}

	if brkAddr, _ := strconv.ParseUint(trimHex(*brk), 16, 64); brkAddr != 0 {
		if err := c.SetBreakpoint(ctx, brkAddr, 0); err != nil {
			fmt.Printf("brk set @%#x: %v\n", brkAddr, err)
		} else {
			fmt.Printf("breakpoint set @%#x OK; continuing...\n", brkAddr)
			readPC := func() uint64 {
				if pcb, err := c.ReadRegisters(ctx, 0x100, 8); err == nil && len(pcb) == 8 {
					return uint64(pcb[0]) | uint64(pcb[1])<<8 | uint64(pcb[2])<<16 | uint64(pcb[3])<<24 |
						uint64(pcb[4])<<32 | uint64(pcb[5])<<40 | uint64(pcb[6])<<48 | uint64(pcb[7])<<56
				}
				return 0
			}
			st, err := c.Continue(ctx)
			if err != nil {
				fmt.Println("continue:", err)
			} else {
				fmt.Printf("STOPPED: code=%#x pid=%d tid=%d raw=%s\n", st.Code, st.PID, st.TID, st.Raw)
				fmt.Printf("PC at stop = %#x (expected %#x)\n", readPC(), brkAddr)
				// Clear the breakpoint before stepping so we advance off it.
				if err := c.ClearBreakpoint(ctx, brkAddr); err != nil {
					fmt.Println("clear before step:", err)
				}
				if sst, err := c.Step(ctx, 1); err != nil {
					fmt.Println("step:", err)
				} else {
					fmt.Printf("STEPPED: code=%#x pc=%#x (was %#x)\n", sst.Code, readPC(), brkAddr)
				}
			}
		}
	}

	if err := c.Detach(ctx, *pid); err != nil {
		fmt.Println("detach:", err)
	} else {
		fmt.Println("detached")
	}
	_ = slog.LevelInfo
}

func trimHex(s string) string {
	if len(s) > 2 && (s[:2] == "0x" || s[:2] == "0X") {
		return s[2:]
	}
	return s
}
