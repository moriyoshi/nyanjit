package cpuprofile

import (
	"encoding/binary"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"golang.org/x/sys/cpu"
	"runtime"
	"slices"
)

// The bootstrap is integer-only CPUID code. Mapping uses the existing pure-Go
// execution facility; it preserves RBX/EBX and does not require an optional ISA.
func detectX86() abi.CPUProfile {
	architecture := runtime.GOARCH
	baseline := x86FallbackProfile(architecture)
	code := []byte{0x53, 0x89, 0xf0, 0x89, 0xd1, 0x49, 0x89, 0xf8, 0x0f, 0xa2, 0x41, 0x89, 0x00, 0x41, 0x89, 0x58, 0x04, 0x41, 0x89, 0x48, 0x08, 0x41, 0x89, 0x50, 0x0c, 0x5b, 0xc3}
	if architecture == "386" {
		// cdecl: output pointer, leaf and subleaf are one stack word each.
		code = []byte{0x53, 0x56, 0x8b, 0x44, 0x24, 0x10, 0x8b, 0x4c, 0x24, 0x14, 0x8b, 0x74, 0x24, 0x0c, 0x0f, 0xa2, 0x89, 0x06, 0x89, 0x5e, 0x04, 0x89, 0x4e, 0x08, 0x89, 0x56, 0x0c, 0x5e, 0x5b, 0xc3}
	}
	probe, err := execution.NewCode(code, execution.CodeOptions{ABI: abi.C, Arity: 3}, nil)
	if err != nil {
		return baseline
	}
	defer probe.Close()
	query := func(leaf uintptr) [4]uint32 {
		var data [16]byte
		_, e := probe.Call(execution.Words(&data, leaf, 0)...)
		if e != nil {
			return [4]uint32{}
		}
		return [4]uint32{binary.LittleEndian.Uint32(data[:]), binary.LittleEndian.Uint32(data[4:]), binary.LittleEndian.Uint32(data[8:]), binary.LittleEndian.Uint32(data[12:])}
	}
	maximum := query(0)[0]
	if maximum < 1 {
		return baseline
	}
	one := query(1)
	seven := [4]uint32{}
	if maximum >= 7 {
		seven = query(7)
	}
	extended := [4]uint32{}
	if query(0x80000000)[0] >= 0x80000001 {
		extended = query(0x80000001)
	}
	features := map[string]bool{"pclmulqdq": cpu.X86.HasPCLMULQDQ, "sse2": cpu.X86.HasSSE2 && one[3]&(1<<26) != 0, "sse3": one[2]&1 != 0, "ssse3": one[2]&(1<<9) != 0, "sse4.1": one[2]&(1<<19) != 0, "sse4.2": one[2]&(1<<20) != 0, "cx16": one[2]&(1<<13) != 0, "lahf": extended[2]&1 != 0, "popcnt": one[2]&(1<<23) != 0, "avx": cpu.X86.HasAVX, "avx2": cpu.X86.HasAVX2, "bmi1": seven[1]&(1<<3) != 0, "bmi2": seven[1]&(1<<8) != 0, "lzcnt": extended[2]&(1<<5) != 0, "f16c": cpu.X86.HasAVX && one[2]&(1<<29) != 0, "fma": cpu.X86.HasFMA && cpu.X86.HasAVX, "movbe": one[2]&(1<<22) != 0, "xsave": one[2]&(1<<26) != 0, "avx512f": cpu.X86.HasAVX512F, "avx512bw": cpu.X86.HasAVX512BW, "avx512cd": cpu.X86.HasAVX512CD, "avx512dq": cpu.X86.HasAVX512DQ, "avx512vl": cpu.X86.HasAVX512VL}
	p := abi.CPUProfile{Level: baseline.Level}
	if architecture == "amd64" {
		for _, level := range []string{"x86-64-v2", "x86-64-v3", "x86-64-v4"} {
			requested, _ := (abi.CPUProfile{Level: level}).Resolve(architecture)
			good := true
			for _, e := range requested.Extensions {
				good = good && features[e]
			}
			if good {
				p.Level = level
			} else {
				break
			}
		}
	}
	for _, name := range []string{"pclmulqdq", "sse2", "bmi1", "bmi2", "lzcnt", "popcnt"} {
		if features[name] {
			p.Extensions = append(p.Extensions, name)
		}
	}
	if architecture == "amd64" {
		for name, enabled := range features {
			if enabled {
				p.Extensions = append(p.Extensions, name)
			}
		}
	}
	return resolveVerifiedX86Profile(p, architecture, features["sse2"])
}

// A 32-bit process can run on pre-SSE2 hardware; failed discovery must not
// turn the target's minimum requirement into a verified host capability.
func x86FallbackProfile(arch string) abi.CPUProfile {
	p, _ := (abi.CPUProfile{}).Resolve(arch)
	if arch == "386" {
		p.Extensions = nil
	}
	return p
}

func resolveVerifiedX86Profile(p abi.CPUProfile, arch string, sse2 bool) abi.CPUProfile {
	out, err := p.Resolve(arch)
	if err != nil {
		return x86FallbackProfile(arch)
	}
	if arch == "386" && !sse2 {
		out.Extensions = slices.DeleteFunc(out.Extensions, func(s string) bool { return s == "sse2" })
	}
	return out
}
