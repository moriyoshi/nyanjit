//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package execution

import "syscall"

func scratch(size int) ([]byte, error) {
	return syscall.Mmap(-1, 0, size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
}

func freeScratch(mem []byte) error { return syscall.Munmap(mem) }
