// Microbenchmark of the syscall sequence ElfMapper::map/reset performs for a
// one-function module: mmap 2 pages RW, write both (page faults), mprotect
// page 0 RX and page 1 R, then munmap. Used to explain the bind figure.
#include <stdio.h>
#include <string.h>
#include <sys/mman.h>
#include <time.h>
#include <unistd.h>
static double now(void) { struct timespec t; clock_gettime(CLOCK_MONOTONIC, &t); return t.tv_sec * 1e9 + t.tv_nsec; }
int main(void) {
  long ps = getpagesize();
  double s_map = 0, s_fault = 0, s_prot = 0, s_unmap = 0;
  int n = 200000;
  for (int i = 0; i < n; i++) {
    double t0 = now();
    char *p = mmap(0, ps + 64, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    double t1 = now();
    memset(p, 1, 64); memset(p + ps, 1, 64);
    double t2 = now();
    mprotect(p, ps, PROT_READ | PROT_EXEC); mprotect(p + ps, 64, PROT_READ);
    double t3 = now();
    munmap(p, ps + 64);
    double t4 = now();
    s_map += t1 - t0; s_fault += t2 - t1; s_prot += t3 - t2; s_unmap += t4 - t3;
  }
  printf("page=%ld mmap=%.2fus 2xfault=%.2fus 2xmprotect=%.2fus munmap=%.2fus\n", ps,
         s_map / n / 1e3, s_fault / n / 1e3, s_prot / n / 1e3, s_unmap / n / 1e3);
}
