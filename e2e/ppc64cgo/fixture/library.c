#include <errno.h>
#include <stdint.h>
#include <stdlib.h>
// Exercise a real ELFv2 PIC global entry, libc calls, and the thread pointer.
static _Thread_local uint64_t count;
uint64_t native_add(uint64_t value) {
	volatile uint64_t *p = malloc(sizeof(*p));
	if (!p) abort();
	*p = value + 7;
	errno = 37;
	count++;
	uint64_t result = *p;
	free((void *)p);
	if (!count || errno != 37) abort();
	return result;
}
