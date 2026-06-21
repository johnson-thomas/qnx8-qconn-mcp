# qnxbench — a portable multicore benchmark for QNX targets

[`qnxbench.c`](qnxbench.c) is a tiny, dependency-free pthreads CPU benchmark for
comparing **behaviour and performance across QNX 8 targets** with one tool: real
hardware, an emulated QEMU VM (see [`../qemu-test/`](../qemu-test/)), and — once
available — a native-virtualised VM. It is intentionally simple so the same
binary/metric is meaningful everywhere.

## What it measures

A CPU-bound, dependency-chained integer mix (so the compiler can't elide or
vectorise it) is run:

1. on **one thread** (per-core throughput), then
2. on **N threads** (N = online CPUs by default) — multicore throughput.

It reports single-thread `Miter/s`, aggregate `Miter/s`, and the **parallel
speedup / efficiency**, plus a machine-parseable `BENCH …` line for scripting.

```
qcc -Vgcc_ntoaarch64le -O2 -o qnxbench qnxbench.c     # aarch64 target
qcc -Vgcc_ntox86_64    -O2 -o qnxbench qnxbench.c     # x86-64 target
qnxbench [-t threads] [-i iters_millions]             # default: online CPUs, 100M
```

## Behaviour: real aarch64 vs emulated x86-64 QEMU

Representative run (your absolute numbers will vary with the host). Real QNX 8 on
a RaspberryPi400 (4× Cortex-A72 @ ~1.8 GHz, native) versus a QNX 8 **x86-64**
image under `qemu-system-x86_64` **TCG emulation** on an aarch64 host:

| Metric | Real aarch64 (4 cores) | QEMU x86-64 TCG (2 vCPUs) |
|---|---|---|
| Single-thread throughput | **105.8** Miter/s | **~20** Miter/s |
| Parallel throughput | **422.6** Miter/s | 36.7 Miter/s |
| Speedup over 1 thread | **4.00×** | 1.82× |
| Parallel efficiency | **100 %** | 91 % |

Takeaways:

- **Per-core throughput** on real hardware is ~5× the emulated guest — that gap is
  the cost of full CPU **emulation** (the host is aarch64; an x86-64 guest can't
  use KVM, so QEMU's TCG JIT runs every guest instruction). The guest even reports
  a fictitious ~99 MHz clock; ignore absolute MHz under TCG.
- **Multicore scaling is functionally correct in both.** Real hardware scales
  linearly (4.00×/100 % across 4 physical cores); QEMU 8.2's multi-threaded TCG
  scales well too (91 % across the 2 vCPUs it was given via `-smp`), it is just
  far slower in absolute terms.
- This is **emulated x86-64 vs native aarch64** — a different ISA *and* emulation,
  so it quantifies emulation overhead, not CPU-vs-CPU. The single-thread `Miter/s`
  is the cleanest cross-target number.

## Why keep the tool

When a **native aarch64** QNX VM is available (an aarch64 guest on an aarch64 host
runs under **KVM**, near bare-metal), run the *same* `qnxbench` in it: same ISA +
hardware virtualisation should land close to real hardware per-core and scale
across the vCPUs you assign — directly comparable against the rows above.

### Run it

```bash
# on any reachable QNX target (real, or the QEMU hostfwd):
qnxbench -i 20            # 20M iters/thread, all online CPUs
# scripted: parse the 'BENCH machine=... single_mips=... speedup=...' line
```
