package abi

import (
	"iter"
	"math/bits"
	"strings"
)

// ResolvedCPU is an immutable CPU contract. Its level and feature bitset are
// owned values; copies cannot change a cached profile. Use CPUProfile.Resolved
// to validate a request, and Profile when an independently mutable slice is needed.
// The zero value has no architecture, level or extensions; it represents no
// validated target contract and Supports returns false for it.
type ResolvedCPU struct {
	architecture string
	level        string
	bits         [2]uint64
}

// Resolved validates the request and returns an immutable, cacheable CPU value.
func (p CPUProfile) Resolved(architecture string) (ResolvedCPU, error) {
	return p.resolveCached(architecture)
}

// Architecture returns the validated target architecture (empty for the zero value).
func (p ResolvedCPU) Architecture() string { return p.architecture }

// Level returns the canonical architecture level.
func (p ResolvedCPU) Level() string { return p.level }

// Has reports whether the resolved contract enables name (case-insensitive).
func (p ResolvedCPU) Has(name string) bool {
	n, ok := extensionBit(normalizeCPUName(name))
	return ok && p.bits[n/64]&(uint64(1)<<(n%64)) != 0
}

// Extensions visits enabled extensions in canonical lexical order.
func (p ResolvedCPU) Extensions() iter.Seq[string] {
	return func(yield func(string) bool) {
		for word, set := range p.bits {
			for set != 0 {
				n := bits.TrailingZeros64(set)
				if !yield(cpuExtensionNames[word*64+n]) {
					return
				}
				set &= set - 1
			}
		}
	}
}

// AppendExtensions appends names into caller-owned storage, without exposing cache data.
func (p ResolvedCPU) AppendExtensions(dst []string) []string {
	for word, set := range p.bits {
		for set != 0 {
			n := bits.TrailingZeros64(set)
			dst = append(dst, cpuExtensionNames[word*64+n])
			set &= set - 1
		}
	}
	return dst
}

// Profile returns a detached mutable profile. An empty extension list is non-nil.
func (p ResolvedCPU) Profile() CPUProfile {
	return CPUProfile{Level: p.level, Extensions: p.AppendExtensions(make([]string, 0, bits.OnesCount64(p.bits[0])+bits.OnesCount64(p.bits[1])))}
}
func normalizeCPUName(name string) string {
	for i := 0; i < len(name); i++ {
		if name[i] >= 'A' && name[i] <= 'Z' || name[i] >= 128 {
			return strings.ToLower(name)
		}
	}
	return name
}

var cpuExtensionNames = [...]string{"a", "avx", "avx2", "avx512bw", "avx512cd", "avx512dq", "avx512f", "avx512vl", "bmi1", "bmi2", "c", "ca32", "cx16", "d", "f", "f16c", "fma", "fp", "i", "lahf", "lasx", "lse", "lsx", "lzcnt", "m", "maddld", "movbe", "neon", "pclmulqdq", "popcnt", "sse2", "sse3", "sse4.1", "sse4.2", "ssse3", "supm", "sve", "sve2", "v", "vsx", "xsave", "za128rs", "za64rs", "zawrs", "zba", "zbb", "zbs", "zcb", "zcmop", "zfa", "zfhmin", "zic64b", "zicbom", "zicbop", "zicboz", "ziccamoa", "ziccif", "zicclsm", "ziccrse", "zicntr", "zicond", "zicsr", "zifencei", "zihintntl", "zihintpause", "zihpm", "zimop", "zkt", "zvbb", "zvfhmin", "zvkt"}

// Supports reports whether this contract meets all requested features and POWER level.
func (p ResolvedCPU) Supports(request ResolvedCPU) bool {
	if p.architecture == "" || p.architecture != request.architecture {
		return false
	}
	if rank := powerLevel(request.level); rank != 0 && powerLevel(p.level) < rank {
		return false
	}
	return request.bits[0]&^p.bits[0] == 0 && request.bits[1]&^p.bits[1] == 0
}
