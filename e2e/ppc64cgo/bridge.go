//go:build linux && (ppc64 || ppc64le)

package ppc64cgo

/*
#cgo CFLAGS: -mcpu=power8
#cgo LDFLAGS: -ldl -lpthread
#include <stdint.h>
#include <stdlib.h>
#include <pthread.h>
#include <dlfcn.h>
typedef uint64_t (*wordfn)(uint64_t);
static uintptr_t open_fixture(const char *path) {
	void *h = dlopen(path, RTLD_NOW | RTLD_LOCAL);
	return h ? (uintptr_t)dlsym(h, "native_add") : 0;
}
static uint64_t invoke(uintptr_t fn, uint64_t x) {
	return ((wordfn)fn)(x);
}
struct request { uintptr_t fn; uint64_t arg; uint64_t result; };
static void *thread_call(void *ptr) {
	struct request *r = ptr;
	r->result = ((wordfn)r->fn)(r->arg);
	// Reuse the attached runtime thread on a second callback.
	r->result = ((wordfn)r->fn)(r->result);
	return NULL;
}
static uint64_t invoke_thread(uintptr_t fn, uint64_t x) {
	struct request r = {fn, x, 0};
	pthread_t thread;
	if (pthread_create(&thread, NULL, thread_call, &r)) abort();
	if (pthread_join(thread, NULL)) abort();
	return r.result;
}
*/
import "C"

import "unsafe"

func fixtureAddress(path string) uintptr {
	s := C.CString(path)
	defer C.free(unsafe.Pointer(s))
	return uintptr(C.open_fixture(s))
}
func invokeCallback(fn uintptr, x uint64) uint64 {
	return uint64(C.invoke(C.uintptr_t(fn), C.uint64_t(x)))
}
func invokeThreadCallback(fn uintptr, x uint64) uint64 {
	return uint64(C.invoke_thread(C.uintptr_t(fn), C.uint64_t(x)))
}
