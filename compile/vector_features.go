package compile

import (
	"fmt"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"runtime"
)

// Lowering records how a semantic SIMD operation was legalized.
type Lowering = abi.VectorLowering

const (
	Native     = abi.Native
	Expanded   = abi.Expanded
	Split      = abi.Split
	Scalarized = abi.Scalarized
)

// VectorInfo describes the selected SIMD baseline and estimates operation lowering.
type VectorInfo struct {
	PreferredBits int
	Features      []string
	// Err records invalid explicit feature requests. Lowering remains scalarized on error.
	Err error
	// Lowering requires an input vector shape and condition for OpVCompare.
	// Omitting that condition gives a conservative Scalarized answer.
	Lowering func(ir.Opcode, ir.Type, ...ir.BranchCondition) Lowering
}

// Vectors describes host or explicit-target SIMD capabilities. Err reports invalid features.
func Vectors(options abi.CompileOptions) VectorInfo {
	arch := options.Architecture
	if arch == "" {
		arch = runtime.GOARCH
	}
	features := append([]string(nil), options.Features...)
	if options.Features == nil {
		profile := resolvedCPU(options)
		switch arch {
		case "amd64":
			if profile.Has("sse2") {
				features = []string{"sse2"}
			}
		case "arm64":
			if profile.Has("neon") {
				features = []string{"neon"}
			}
		}
	}
	enabled := false
	for _, f := range features {
		enabled = enabled || arch == "amd64" && f == "sse2" || arch == "arm64" && f == "neon"
	}
	info := VectorInfo{Features: features, Err: validateVectorFeatures(options)}
	if enabled && info.Err == nil {
		info.PreferredBits = 128
	}
	info.Lowering = func(op ir.Opcode, t ir.Type, condition ...ir.BranchCondition) Lowering {
		if info.Err != nil || !enabled || !t.Valid() {
			return Scalarized
		}
		if op == ir.OpVCompare && (len(condition) != 1 || condition[0] != ir.BranchEqual || t != ir.Vec(16, 8)) {
			return Scalarized
		}
		if t.Kind == ir.MaskKind {
			if op == ir.OpVMaskBits && t.Lanes == 16 {
				if arch == "arm64" {
					return Expanded
				}
				return Native
			}
			return Scalarized
		}
		if t.Kind != ir.VectorKind {
			return Scalarized
		}
		if t.Bits() > 128 && t.Bits()%128 == 0 {
			small := ir.Vec(128/uint16(t.LaneBits), t.LaneBits)
			if nativeVectorOperation(arch, op, small) {
				return Split
			}
		}
		if t.Bits() == 128 && nativeVectorOperation(arch, op, t) {
			if op == ir.OpVSplat && arch == "amd64" {
				return Expanded
			}
			return Native
		}
		return Scalarized
	}
	return info
}
func nativeVectorOperation(arch string, op ir.Opcode, t ir.Type) bool {
	switch op {
	case ir.OpVLoad, ir.OpVStore, ir.OpVSplat, ir.OpVConst, ir.OpVExtract, ir.OpVZero, ir.OpVInsertZero, ir.OpAdd, ir.OpSub, ir.OpAnd, ir.OpOr, ir.OpXor:
		return true
	case ir.OpMul:
		return arch == "amd64" && t.LaneBits == 16 || arch == "arm64" && t.LaneBits <= 32
	case ir.OpVInsert:
		return arch == "arm64" || t.LaneBits == 16
	case ir.OpVCompare:
		return t.LaneBits == 8
	}
	return false
}
func validateVectorFeatures(options abi.CompileOptions) error {
	arch := options.Architecture
	if arch == "" {
		arch = runtime.GOARCH
	}
	for _, f := range options.Features {
		if !(f == "sse2" && arch == "amd64" || f == "neon" && arch == "arm64") {
			return fmt.Errorf("SIMD feature %q is unavailable for %s: %w", f, arch, ErrBackendLimit)
		}
	}
	return nil
}
