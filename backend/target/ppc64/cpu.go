package ppc64

import (
	"encoding/binary"
	"os"
	"runtime"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"golang.org/x/sys/cpu"
)

// HostCPU reports the POWER level enabled by the executing Linux kernel.
// POWER8 is the Go platform baseline. Missing auxv access conservatively
// disables newer instructions; /proc/cpuinfo is not used because QEMU user
// mode exposes the host's CPU there.
func HostCPU() (abi.PPC64CPU, error) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "ppc64le" {
		return 0, abi.ErrInvalidSpec
	}
	level := abi.PPC64Power8
	if cpu.PPC64.IsPOWER9 {
		level = abi.PPC64Power9
	}
	auxv, err := os.ReadFile("/proc/self/auxv")
	if err != nil {
		return level, nil
	}
	return cpuFromAuxv(auxv, level), nil
}

// Linux AT_HWCAP2=26. ISA bits agree with runtime's
// internal/cpu/cpu_ppc64x_linux.go: ARCH_3_00 and ARCH_3_1.
func cpuFromAuxv(auxv []byte, baseline abi.PPC64CPU) abi.PPC64CPU {
	for len(auxv) >= 16 {
		tag, value := binary.LittleEndian.Uint64(auxv), binary.LittleEndian.Uint64(auxv[8:])
		if tag == 0 {
			break
		}
		if tag == 26 {
			if value&0x00040000 != 0 {
				return abi.PPC64Power10
			}
			if value&0x00800000 != 0 {
				return abi.PPC64Power9
			}
			break
		}
		auxv = auxv[16:]
	}
	return baseline
}
