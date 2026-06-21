/*
 * qnxbench - a small portable multicore CPU benchmark for QNX targets.
 *
 * It runs a fixed amount of CPU-bound integer work first on a single thread,
 * then on N threads (one per online CPU by default), and reports the throughput
 * and the parallel speedup/efficiency. The same binary/metric is meant to be run
 * unchanged on different QNX targets (real aarch64 hardware, an emulated x86_64
 * QEMU VM, a future native aarch64 KVM VM) so their behaviour can be compared.
 *
 * Build (QNX SDK):
 *     qcc -Vgcc_ntoaarch64le -O2 -o qnxbench qnxbench.c
 *     qcc -Vgcc_ntox86_64    -O2 -o qnxbench qnxbench.c
 *
 * Usage: qnxbench [-t threads] [-i iters_millions]
 */
#include <errno.h>
#include <inttypes.h>
#include <pthread.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/utsname.h>
#include <time.h>
#include <unistd.h>

enum { MILLION = 1000000 };
enum { K_MAX_THREADS = 256 };       /* compile-time constant: arrays are not VLAs */
enum { DEFAULT_ITER_MILLIONS = 100 }; /* work units per thread */
static const double NSEC_PER_SEC = 1.0e9;

/* Per-thread state: how much work to do, where to record the timing/result. */
typedef struct {
    uint64_t iters;   /* number of mix() iterations to perform */
    uint64_t result;  /* sink, so the work cannot be optimised away */
    double seconds;   /* wall-clock time this thread spent in the loop */
} worker_t;

/* A cheap, dependency-chained integer mix (MurmurHash3 finaliser). Each
 * iteration depends on the previous one, so the compiler cannot vectorise or
 * elide it, making this a stable per-core ALU benchmark. */
static uint64_t mix(uint64_t value) {
    uint64_t result = value;
    result ^= result >> 33;
    result *= UINT64_C(0xff51afd7ed558ccd);
    result ^= result >> 33;
    result *= UINT64_C(0xc4ceb9fe1a85ec53);
    result ^= result >> 33;
    return result;
}

static double now_seconds(void) {
    struct timespec now = {0, 0};
    if (clock_gettime(CLOCK_MONOTONIC, &now) != 0) {
        return 0.0;
    }
    return (double)now.tv_sec + ((double)now.tv_nsec / NSEC_PER_SEC);
}

static void *worker(void *arg) {
    worker_t *self = (worker_t *)arg;
    const double start = now_seconds();
    uint64_t acc = UINT64_C(0x123456789abcdef);
    for (uint64_t i = 0; i < self->iters; ++i) {
        acc = mix(acc + i);
    }
    self->seconds = now_seconds() - start;
    self->result = acc;
    return NULL;
}

/* Run `count` worker threads, each doing `iters` iterations. Returns the wall
 * time for the whole parallel region, or a negative value on error. */
static double run_phase(int count, uint64_t iters, uint64_t *sink) {
    pthread_t threads[K_MAX_THREADS];
    worker_t args[K_MAX_THREADS];
    if ((count < 1) || (count > K_MAX_THREADS)) {
        return -1.0;
    }
    for (int i = 0; i < count; ++i) {
        args[i].iters = iters;
        args[i].result = 0;
        args[i].seconds = 0.0;
    }
    const double start = now_seconds();
    int started = 0;
    for (int i = 0; i < count; ++i) {
        if (pthread_create(&threads[i], NULL, &worker, &args[i]) != 0) {
            break;
        }
        ++started;
    }
    for (int i = 0; i < started; ++i) {
        (void)pthread_join(threads[i], NULL);
    }
    const double elapsed = now_seconds() - start;
    if (started != count) {
        return -1.0;
    }
    for (int i = 0; i < count; ++i) {
        *sink ^= args[i].result;
    }
    return elapsed;
}

/* Parse a positive integer argument; returns 0 on failure. */
static uint64_t parse_u64(const char *text) {
    char *end = NULL;
    unsigned long long value = 0;
    errno = 0;
    value = strtoull(text, &end, 10);
    if (errno != 0) {
        return 0;
    }
    if ((end == text) || (*end != '\0')) {
        return 0;
    }
    return (uint64_t)value;
}

int main(int argc, char *argv[]) {
    const long online = sysconf(_SC_NPROCESSORS_ONLN);
    int threads = ((online > 0) && (online <= K_MAX_THREADS)) ? (int)online : 1;
    uint64_t iter_millions = (uint64_t)DEFAULT_ITER_MILLIONS;

    int opt = getopt(argc, argv, "t:i:");
    while (opt != -1) {
        if (opt == (int)'t') {
            threads = (int)parse_u64(optarg);
        } else if (opt == (int)'i') {
            iter_millions = parse_u64(optarg);
        } else {
            return EXIT_FAILURE;
        }
        opt = getopt(argc, argv, "t:i:");
    }
    if ((threads < 1) || (threads > K_MAX_THREADS) || (iter_millions == 0U)) {
        return EXIT_FAILURE;
    }
    const uint64_t iters = iter_millions * (uint64_t)MILLION;

    struct utsname uts = {0};
    (void)uname(&uts);

    uint64_t sink = 0;
    const double t_single = run_phase(1, iters, &sink);
    const double t_parallel = run_phase(threads, iters, &sink);
    if ((t_single <= 0.0) || (t_parallel <= 0.0)) {
        return EXIT_FAILURE;
    }

    const double single_mips = (double)iters / t_single / (double)MILLION;
    const double parallel_mips =
        ((double)iters * (double)threads) / t_parallel / (double)MILLION;
    const double speedup = parallel_mips / single_mips;
    const double efficiency = speedup / (double)threads;

    (void)printf("qnxbench: %s %s %s, online CPUs=%ld\n", uts.sysname, uts.release,
                 uts.machine, online);
    (void)printf("  work:      %" PRIu64 "M iters/thread, threads=%d\n", iter_millions,
                 threads);
    (void)printf("  single:    %.1f Miter/s  (%.3f s)\n", single_mips, t_single);
    (void)printf("  parallel:  %.1f Miter/s  (%.3f s)\n", parallel_mips, t_parallel);
    (void)printf("  speedup:   %.2fx over 1 thread  (efficiency %.0f%%)\n", speedup,
                 efficiency * 100.0);
    /* Machine-parseable line for scripted comparison across targets. */
    (void)printf("BENCH machine=%s threads=%d iters_m=%" PRIu64
                 " single_mips=%.1f par_mips=%.1f speedup=%.2f eff=%.2f\n",
                 (uts.machine[0] != '\0') ? uts.machine : "?", threads, iter_millions,
                 single_mips, parallel_mips, speedup, efficiency);
    (void)sink; /* keep the optimiser honest */
    return EXIT_SUCCESS;
}
