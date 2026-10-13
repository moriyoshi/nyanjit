package main

import (
	"encoding/binary"
	"fmt"
)

const (
	pageSize        = 0x10000 // Keep segments separate on 64 KiB page kernels.
	imageBase       = 0x400000
	stubSize        = 16
	codeCapacity    = 4096
	readOnlyOffset  = 2 * pageSize
	readOnlyAddress = imageBase + readOnlyOffset
)

// encodeExecutable wraps the example's emitted C ABI function in a fixed-address
// Linux ELF64 image. The entry stub calls the function and exits with its result.
func encodeExecutable(architecture string, code, message []byte) ([]byte, error) {
	if len(code) == 0 || len(code) > codeCapacity || len(message) == 0 {
		return nil, fmt.Errorf("invalid ELF example payload")
	}
	var stub []byte
	var machine uint16
	switch architecture {
	case "amd64":
		machine = 62 // EM_X86_64
		stub = []byte{0xE8, 0x0B, 0, 0, 0, 0x48, 0x89, 0xC7, 0xB8, 0x3C, 0, 0, 0, 0x0F, 0x05, 0xCC}
	case "arm64":
		machine = 183 // EM_AARCH64
		stub = make([]byte, stubSize)
		for i, word := range [...]uint32{0x94000004, 0xD2800BA8, 0xD4000001, 0xD4200000} {
			binary.LittleEndian.PutUint32(stub[i*4:], word)
		}
	case "riscv64":
		machine = 243 // EM_RISCV
		stub = make([]byte, stubSize)
		// jal ra, code; addi a7, zero, SYS_exit; ecall; ebreak.
		for i, word := range [...]uint32{0x010000ef, 0x05d00893, 0x00000073, 0x00100073} {
			binary.LittleEndian.PutUint32(stub[i*4:], word)
		}
	default:
		return nil, fmt.Errorf("unsupported ELF architecture %q", architecture)
	}
	image := make([]byte, readOnlyOffset+len(message))
	copy(image[pageSize:], stub)
	copy(image[pageSize+stubSize:], code)
	copy(image[readOnlyOffset:], message)
	copy(image[:4], "\x7fELF")
	image[4], image[5], image[6] = 2, 1, 1 // ELF64, little-endian, version 1
	put16 := func(at int, value uint16) { binary.LittleEndian.PutUint16(image[at:], value) }
	put32 := func(at int, value uint32) { binary.LittleEndian.PutUint32(image[at:], value) }
	put64 := func(at int, value uint64) { binary.LittleEndian.PutUint64(image[at:], value) }
	put16(16, 2) // ET_EXEC
	put16(18, machine)
	put32(20, 1)
	put64(24, imageBase+pageSize)
	put64(32, 64) // Program header table follows the ELF header.
	put16(52, 64)
	put16(54, 56)
	put16(56, 2)
	writeSegment := func(index int, flags uint32, offset, length int) {
		at := 64 + index*56
		put32(at, 1) // PT_LOAD
		put32(at+4, flags)
		put64(at+8, uint64(offset))
		put64(at+16, imageBase+uint64(offset))
		put64(at+24, imageBase+uint64(offset))
		put64(at+32, uint64(length))
		put64(at+40, uint64(length))
		put64(at+48, pageSize)
	}
	writeSegment(0, 5, 0, pageSize+stubSize+len(code)) // PF_R | PF_X
	writeSegment(1, 4, readOnlyOffset, len(message))   // PF_R
	return image, nil
}
