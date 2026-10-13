package abi

import (
	"fmt"
	"slices"
	"strings"
)

// CPUProfile declares the minimum ISA level and enabled instruction extensions.
// Empty Level is the target baseline; Extensions are normalized and detached by Resolve.
type CPUProfile struct {
	Level      string
	Extensions []string
}

// Clone returns an independent profile.
func (p CPUProfile) Clone() CPUProfile {
	p.Extensions = append([]string(nil), p.Extensions...)
	return p
}

// Has reports an enabled extension in a resolved profile.
func (p CPUProfile) Has(extension string) bool {
	return slices.Contains(p.Extensions, strings.ToLower(extension))
}

// Resolve validates a profile for architecture and includes mandatory level extensions.
func (p CPUProfile) Resolve(architecture string) (CPUProfile, error) {
	level := strings.ToLower(p.Level)
	base := []string{}
	allowed := []string{}
	switch architecture {
	case "amd64":
		if level == "" {
			level = "x86-64-v1"
		}
		allowed = []string{"sse2", "sse3", "ssse3", "sse4.1", "sse4.2", "cx16", "lahf", "popcnt", "avx", "avx2", "bmi1", "bmi2", "lzcnt", "f16c", "fma", "movbe", "xsave", "avx512f", "avx512bw", "avx512cd", "avx512dq", "avx512vl"}
		if level != "x86-64-v1" && level != "x86-64-v2" && level != "x86-64-v3" && level != "x86-64-v4" {
			return CPUProfile{}, ErrInvalidSpec
		}
		base = []string{"sse2"}
		if level >= "x86-64-v2" {
			base = append(base, "sse3", "ssse3", "sse4.1", "sse4.2", "cx16", "lahf", "popcnt")
		}
		if level >= "x86-64-v3" {
			base = append(base, "avx", "avx2", "bmi1", "bmi2", "lzcnt", "f16c", "fma", "movbe", "xsave")
		}
		if level == "x86-64-v4" {
			base = append(base, "avx512f", "avx512bw", "avx512cd", "avx512dq", "avx512vl")
		}
	case "386":
		if level == "" {
			level = "i686"
		}
		if level != "i686" {
			return CPUProfile{}, ErrInvalidSpec
		}
		allowed = []string{"sse2", "bmi1", "bmi2", "lzcnt", "popcnt"}
	case "arm64":
		if level == "" {
			level = "armv8.0-a"
		}
		if level != "armv8.0-a" {
			return CPUProfile{}, ErrInvalidSpec
		}
		base = []string{"fp", "neon"}
		allowed = []string{"fp", "neon", "lse", "sve", "sve2"}
	case "riscv64":
		if level == "" {
			level = "rv64gc"
		}
		allowed = []string{"i", "m", "a", "f", "d", "c", "zicsr", "zifencei", "zicntr", "zihpm", "ziccif", "ziccrse", "ziccamoa", "zicclsm", "za128rs", "za64rs", "zic64b", "supm", "zihintpause", "zba", "zbb", "zbs", "zicbom", "zicbop", "zicboz", "zfhmin", "zkt", "v", "zvfhmin", "zvbb", "zvkt", "zihintntl", "zicond", "zimop", "zcmop", "zcb", "zfa", "zawrs"}
		base = []string{"i", "m", "a", "f", "d", "c", "zicsr", "zifencei"}
		switch level {
		case "rv64gc":
		case "rva20", "rva20u64":
			level = "rva20u64"
			base = append(base, "zicntr", "ziccif", "ziccrse", "ziccamoa", "zicclsm", "za128rs")
		case "rva22", "rva22u64":
			level = "rva22u64"
			base = append(base, "zicntr", "zihpm", "ziccif", "ziccrse", "ziccamoa", "zicclsm", "za64rs", "zic64b", "zihintpause", "zba", "zbb", "zbs", "zicbom", "zicbop", "zicboz", "zfhmin", "zkt")
		case "rva23", "rva23u64":
			level = "rva23u64"
			base = append(base, "zicntr", "zihpm", "ziccif", "ziccrse", "ziccamoa", "zicclsm", "za64rs", "zic64b", "zihintpause", "zba", "zbb", "zbs", "zicbom", "zicbop", "zicboz", "zfhmin", "zkt", "v", "zvfhmin", "zvbb", "zvkt", "zihintntl", "zicond", "zimop", "zcmop", "zcb", "zfa", "zawrs", "supm")
		default:
			return CPUProfile{}, ErrInvalidSpec
		}
	case "ppc64", "ppc64le":
		if level == "" {
			level = "power8"
		}
		if level != "power8" && level != "power9" && level != "power10" {
			return CPUProfile{}, ErrInvalidSpec
		}
		allowed = []string{"vsx", "maddld", "ca32"}
		base = []string{"vsx"}
		if level != "power8" {
			base = append(base, "maddld", "ca32")
		}
	default:
		return CPUProfile{}, ErrInvalidSpec
	}
	for _, extension := range p.Extensions {
		extension = strings.ToLower(extension)
		if !slices.Contains(allowed, extension) {
			return CPUProfile{}, fmt.Errorf("CPU extension %q is invalid for %s: %w", extension, architecture, ErrInvalidSpec)
		}
		base = append(base, extension)
	}
	if architecture == "ppc64" || architecture == "ppc64le" {
		if level == "power8" && (slices.Contains(base, "maddld") || slices.Contains(base, "ca32")) {
			return CPUProfile{}, fmt.Errorf("maddld and ca32 require power9 or newer: %w", ErrInvalidSpec)
		}
	}
	if architecture == "amd64" {
		for _, extension := range base {
			if strings.HasPrefix(extension, "avx512") {
				base = append(base, "avx512f")
				break
			}
		}
		if slices.Contains(base, "fma") || slices.Contains(base, "f16c") || slices.Contains(base, "avx2") || slices.Contains(base, "avx512f") {
			base = append(base, "avx")
		}
		if slices.Contains(base, "avx") {
			base = append(base, "xsave")
		}
	}
	if architecture == "arm64" && slices.Contains(base, "sve2") {
		base = append(base, "sve")
	}
	slices.Sort(base)
	base = slices.Compact(base)
	return CPUProfile{Level: level, Extensions: base}, nil
}

// Supports compares resolved profiles, requiring the requested POWER level
// and all of the requested level's instruction extensions.
// Non-instruction profile mandates that the kernel cannot expose remain conservative.
func (p CPUProfile) Supports(request CPUProfile) bool {
	// POWER9 and POWER10 currently expose the same optional instructions to
	// selection. Their declared minimum CPU levels still differ: accepting a
	// POWER10 profile on POWER9 would silently discard that requirement.
	if rank := powerLevel(request.Level); rank != 0 && powerLevel(p.Level) < rank {
		return false
	}
	for _, extension := range request.Extensions {
		if !p.Has(extension) {
			return false
		}
	}
	return true
}

func powerLevel(level string) int {
	switch level {
	case "power8":
		return 8
	case "power9":
		return 9
	case "power10":
		return 10
	}
	return 0
}
