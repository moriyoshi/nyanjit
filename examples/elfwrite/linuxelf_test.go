package main

import (
	"bytes"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEncodeExecutable(t *testing.T) {
	for _, tc := range []struct {
		architecture string
		machine      elf.Machine
	}{
		{"amd64", elf.EM_X86_64},
		{"arm64", elf.EM_AARCH64},
		{"riscv64", elf.EM_RISCV},
	} {
		t.Run(tc.architecture, func(t *testing.T) {
			payload := []byte("hello\n")
			image, err := encodeExecutable(tc.architecture, []byte{0xc3}, payload)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := elf.NewFile(bytes.NewReader(image))
			if err != nil {
				t.Fatal(err)
			}
			defer parsed.Close()
			if parsed.Type != elf.ET_EXEC || parsed.Machine != tc.machine ||
				parsed.Entry != imageBase+pageSize || len(parsed.Progs) != 2 ||
				parsed.Progs[0].Flags != elf.PF_R|elf.PF_X || parsed.Progs[1].Flags != elf.PF_R ||
				parsed.Progs[1].Vaddr != readOnlyAddress || parsed.Progs[1].Filesz != uint64(len(payload)) {
				t.Fatalf("unexpected ELF image: header=%+v programs=%+v", parsed.FileHeader, parsed.Progs)
			}
		})
	}
}

func TestExecutableRuns(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux executable")
	}
	var code []byte
	switch runtime.GOARCH {
	case "amd64":
		code = []byte{0x31, 0xc0, 0xc3} // xor eax, eax; ret
	case "arm64":
		code = []byte{0x00, 0x00, 0x80, 0xd2, 0xc0, 0x03, 0x5f, 0xd6} // mov x0, #0; ret
	case "riscv64":
		code = []byte{0x13, 0x05, 0x00, 0x00, 0x67, 0x80, 0x00, 0x00} // addi a0, zero, 0; ret
	default:
		t.Skip("unsupported architecture")
	}
	image, err := encodeExecutable(runtime.GOARCH, code, []byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "generated")
	if err := os.WriteFile(path, image, 0755); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(path).CombinedOutput(); err != nil {
		t.Fatalf("generated executable failed: %v: %s", err, output)
	}
}
