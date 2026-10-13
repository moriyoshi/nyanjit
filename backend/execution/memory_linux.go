//go:build linux && (386 || amd64 || arm64 || loong64 || ppc64le || riscv64)

package execution

import "syscall"

func mapCode(code []byte, relocate func([]byte) error) ([]byte, error) {
	mem, err := syscall.Mmap(-1, 0, len(code), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		return nil, err
	}
	if err := fillCode(mem, code, relocate); err != nil {
		_ = syscall.Munmap(mem)
		return nil, err
	}
	return mem, nil
}

// fillCode copies code into the writable, page-aligned mem, relocates it in
// place and makes mem executable.
func fillCode(mem, code []byte, relocate func([]byte) error) error {
	if err := writeCode(mem, code, relocate); err != nil {
		return err
	}
	return makeExecutable(mem, len(code))
}

func unmapCode(mem []byte) error { return syscall.Munmap(mem) }

func pageSize() int { return syscall.Getpagesize() }

// reserveArena reserves inaccessible address space without committing memory.
func reserveArena(size int) ([]byte, error) {
	return syscall.Mmap(-1, 0, size, syscall.PROT_NONE, syscall.MAP_PRIVATE|syscall.MAP_ANON|syscall.MAP_NORESERVE)
}

func unreserveArena(mem []byte) error { return syscall.Munmap(mem) }

// commitPages makes arena pages writable for fillCode.
func commitPages(pages []byte) error {
	return syscall.Mprotect(pages, syscall.PROT_READ|syscall.PROT_WRITE)
}

// decommitPages makes arena pages inaccessible and discards their contents.
func decommitPages(pages []byte) error {
	if err := syscall.Mprotect(pages, syscall.PROT_NONE); err != nil {
		return err
	}
	return syscall.Madvise(pages, syscall.MADV_DONTNEED)
}
