#include <stdio.h>
#include <unistd.h>

int compute(int a, int b) {
    int r = a * b + 7;   /* good spot for a breakpoint */
    return r;
}

int main(void) {
    int i = 0;
    setvbuf(stdout, NULL, _IONBF, 0);
    for (;;) {
        int v = compute(i, i + 1);
        printf("tick %d -> %d\n", i, v);
        i++;
        sleep(2);
    }
    return 0;
}
