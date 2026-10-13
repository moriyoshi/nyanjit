package abi

import (
	"fmt"
	"slices"
	"strings"
	"sync"
)

// CPUProfile declares the minimum ISA level and enabled instruction extensions.
// Empty Level is the target baseline; Extensions are normalized and detached by Resolve.
type CPUProfile struct {
	Level      string
	Extensions []string
}

// Profile metadata is cached separately so CPUProfile remains plain public data.
// Entries own their slices; Resolve never returns cached storage to callers.
type profileKey struct {
	architecture, level string
	extensions          [2]uint64
}

var profiles = struct {
	sync.RWMutex
	values map[profileKey]ResolvedCPU
}{values: make(map[profileKey]ResolvedCPU)}

func requestKey(p CPUProfile, architecture string) (profileKey, bool) {
	key := profileKey{architecture: architecture, level: normalizeCPUName(p.Level)}
	for _, name := range p.Extensions {
		bit, ok := extensionBit(normalizeCPUName(name))
		if !ok {
			return profileKey{}, false
		}
		key.extensions[bit/64] |= uint64(1) << (bit % 64)
	}
	return key, true
}

func extensionBit(name string) (uint, bool) {
	switch name {
	case "a":
		return 0, true
	case "avx":
		return 1, true
	case "avx2":
		return 2, true
	case "avx512bw":
		return 3, true
	case "avx512cd":
		return 4, true
	case "avx512dq":
		return 5, true
	case "avx512f":
		return 6, true
	case "avx512vl":
		return 7, true
	case "bmi1":
		return 8, true
	case "bmi2":
		return 9, true
	case "c":
		return 10, true
	case "ca32":
		return 11, true
	case "cx16":
		return 12, true
	case "d":
		return 13, true
	case "f":
		return 14, true
	case "f16c":
		return 15, true
	case "fma":
		return 16, true
	case "fp":
		return 17, true
	case "i":
		return 18, true
	case "lahf":
		return 19, true
	case "lasx":
		return 20, true
	case "lse":
		return 21, true
	case "lsx":
		return 22, true
	case "lzcnt":
		return 23, true
	case "m":
		return 24, true
	case "maddld":
		return 25, true
	case "movbe":
		return 26, true
	case "neon":
		return 27, true
	case "pclmulqdq":
		return 28, true
	case "popcnt":
		return 29, true
	case "sse2":
		return 30, true
	case "sse3":
		return 31, true
	case "sse4.1":
		return 32, true
	case "sse4.2":
		return 33, true
	case "ssse3":
		return 34, true
	case "supm":
		return 35, true
	case "sve":
		return 36, true
	case "sve2":
		return 37, true
	case "v":
		return 38, true
	case "vsx":
		return 39, true
	case "xsave":
		return 40, true
	case "za128rs":
		return 41, true
	case "za64rs":
		return 42, true
	case "zawrs":
		return 43, true
	case "zba":
		return 44, true
	case "zbb":
		return 45, true
	case "zbs":
		return 46, true
	case "zcb":
		return 47, true
	case "zcmop":
		return 48, true
	case "zfa":
		return 49, true
	case "zfhmin":
		return 50, true
	case "zic64b":
		return 51, true
	case "zicbom":
		return 52, true
	case "zicbop":
		return 53, true
	case "zicboz":
		return 54, true
	case "ziccamoa":
		return 55, true
	case "ziccif":
		return 56, true
	case "zicclsm":
		return 57, true
	case "ziccrse":
		return 58, true
	case "zicntr":
		return 59, true
	case "zicond":
		return 60, true
	case "zicsr":
		return 61, true
	case "zifencei":
		return 62, true
	case "zihintntl":
		return 63, true
	case "zihintpause":
		return 64, true
	case "zihpm":
		return 65, true
	case "zimop":
		return 66, true
	case "zkt":
		return 67, true
	case "zvbb":
		return 68, true
	case "zvfhmin":
		return 69, true
	case "zvkt":
		return 70, true
	}
	return 0, false
}

// Clone returns an independent profile, preserving nil versus non-nil empty extensions.
func (p CPUProfile) Clone() CPUProfile {
	p.Extensions = slices.Clone(p.Extensions)
	return p
}

// Has reports an enabled extension in a resolved profile.
func (p CPUProfile) Has(extension string) bool {
	extension = normalizeCPUName(extension)
	// Resolve sorts its output. Retain the fallback for mutable request values
	// and callers that reorder or append to a resolved profile.
	if len(p.Extensions) > 8 {
		if _, found := slices.BinarySearch(p.Extensions, extension); found {
			return true
		}
	}
	return slices.Contains(p.Extensions, extension)
}

// Resolve validates a profile for architecture and includes mandatory level extensions.
func (p CPUProfile) Resolve(architecture string) (CPUProfile, error) {
	resolved, err := p.resolveCached(architecture)
	if err != nil {
		return CPUProfile{}, err
	}
	return resolved.Profile(), nil
}

func (p CPUProfile) resolveCached(architecture string) (ResolvedCPU, error) {
	key, cacheable := requestKey(p, architecture)
	if cacheable {
		profiles.RLock()
		cached, ok := profiles.values[key]
		profiles.RUnlock()
		if ok {
			return cached, nil
		}
	}
	resolved, err := p.resolve(architecture)
	if err != nil {
		return ResolvedCPU{}, err
	}
	canonical := ResolvedCPU{architecture: architecture, level: resolved.Level}
	for _, name := range resolved.Extensions {
		if n, ok := extensionBit(name); ok {
			canonical.bits[n/64] |= uint64(1) << (n % 64)
		} else {
			panic("abi: resolved extension missing from bit table: " + name)
		}
	}
	if cacheable {
		profiles.Lock()
		// Keep at most 512 entries, with no eviction. Later requests still resolve
		// normally but are not cached, bounding arbitrary client combinations.
		if len(profiles.values) < 512 {
			profiles.values[key] = canonical
		}
		profiles.Unlock()
	}
	return canonical, nil
}

func (p CPUProfile) resolve(architecture string) (CPUProfile, error) {
	level := normalizeCPUName(p.Level)
	base := []string{}
	allowed := []string{}
	switch architecture {
	case "amd64":
		if level == "" {
			level = "x86-64-v1"
		}
		allowed = []string{"pclmulqdq", "sse2", "sse3", "ssse3", "sse4.1", "sse4.2", "cx16", "lahf", "popcnt", "avx", "avx2", "bmi1", "bmi2", "lzcnt", "f16c", "fma", "movbe", "xsave", "avx512f", "avx512bw", "avx512cd", "avx512dq", "avx512vl"}
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
		base = []string{"sse2"}
		allowed = []string{"pclmulqdq", "sse2", "bmi1", "bmi2", "lzcnt", "popcnt"}
	case "arm64":
		if level == "" {
			level = "armv8.0-a"
		}
		if level != "armv8.0-a" {
			return CPUProfile{}, ErrInvalidSpec
		}
		base = []string{"fp", "neon"}
		allowed = []string{"fp", "neon", "lse", "sve", "sve2"}
	case "loong64":
		if level == "" {
			level = "la64v1.0"
		}
		if level != "la64v1.0" {
			return CPUProfile{}, ErrInvalidSpec
		}
		allowed = []string{"lsx", "lasx"}
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
	case "ppc64le":
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
		extension = normalizeCPUName(extension)
		if !slices.Contains(allowed, extension) {
			return CPUProfile{}, fmt.Errorf("CPU extension %q is invalid for %s: %w", extension, architecture, ErrInvalidSpec)
		}
		base = append(base, extension)
	}
	if architecture == "ppc64le" {
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
	if architecture == "loong64" && slices.Contains(base, "lasx") {
		base = append(base, "lsx")
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
