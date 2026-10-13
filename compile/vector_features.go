package compile

import (
	"fmt"
	"runtime"
	"slices"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/ir"
)

// VectorInfo describes the selected SIMD baseline and estimates operation lowering.
type VectorInfo struct {
	PreferredBits int
	Features      []string
	// Lowering requires an input vector shape and condition for OpVCompare.
	// Omitting that condition gives a conservative Scalarized answer.
	Lowering func(ir.Opcode, ir.Type, ...ir.BranchCondition) abi.VectorLowering
}

// vectorInfo describes the SIMD baseline for options' target. NewTarget has
// already rejected invalid feature requests.
func vectorInfo(options compileOptions) VectorInfo {
	arch := options.Architecture
	if arch == "" {
		arch = runtime.GOARCH
	}
	features := append([]string(nil), options.Features...)
	if options.Features == nil {
		profile := resolvedFeatures(options)
		switch arch {
		case "amd64", "386":
			if profile.Has("sse2") {
				features = []string{"sse2"}
				if profile.Has("pclmulqdq") {
					features = append(features, "pclmulqdq")
				}
			}
		case "arm64":
			if profile.Has("neon") {
				features = []string{"neon"}
			}
		case "loong64":
			if profile.Has("lsx") {
				features = []string{"lsx"}
			}
		}
	}
	// Carryless multiplication uses the SSE2 register file, even when callers
	// request only its optional extension by name.
	if (arch == "amd64" || arch == "386") && slices.Contains(features, "pclmulqdq") && !slices.Contains(features, "sse2") {
		features = append([]string{"sse2"}, features...)
	}
	if arch == "loong64" && slices.Contains(features, "lasx") && !slices.Contains(features, "lsx") {
		features = append([]string{"lsx"}, features...)
	}
	enabled := false
	for _, f := range features {
		enabled = enabled || (arch == "amd64" || arch == "386") && f == "sse2" || arch == "arm64" && f == "neon" || arch == "loong64" && f == "lsx"
	}
	// Unchecked internal callers can still pass an invalid request; it lowers
	// with scalar lanes.
	valid := validateVectorFeatures(options) == nil
	info := VectorInfo{Features: features}
	if enabled && valid {
		info.PreferredBits = 128
	}
	info.Lowering = func(operation ir.Opcode, t ir.Type, condition ...ir.BranchCondition) abi.VectorLowering {
		if !valid || !enabled || !t.Valid() {
			return abi.Scalarized
		}
		if operation == ir.OpVCompare && (len(condition) != 1 || condition[0] != ir.BranchEqual || t != ir.Vec(16, 8)) {
			return abi.Scalarized
		}
		if t.Kind == ir.MaskKind {
			if operation == ir.OpVMaskBits && t.Lanes == 16 {
				if arch == "arm64" || arch == "loong64" {
					return abi.Expanded
				}
				return abi.Native
			}
			return abi.Scalarized
		}
		if t.Kind != ir.VectorKind {
			return abi.Scalarized
		}
		if t.Bits() > 128 && t.Bits()%128 == 0 {
			small := ir.Vec(128/uint16(t.LaneBits), t.LaneBits)
			if nativeVectorOperation(arch, operation, small, features) {
				return abi.Split
			}
		}
		if t.Bits() == 128 && nativeVectorOperation(arch, operation, t, features) {
			_, byValue := op.ShiftByOperation(operation)
			if operation == ir.OpVSplat && (arch == "amd64" || arch == "386") || arch != "loong64" && (operation == ir.OpRotateLeft || operation == ir.OpRotateRight) || arch == "loong64" && byValue {
				return abi.Expanded
			}
			return abi.Native
		}
		return abi.Scalarized
	}
	return info
}
func nativeVectorOperation(arch string, op ir.Opcode, t ir.Type, features []string) bool {
	if arch == "loong64" {
		switch op {
		case ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic, ir.OpRotateLeft, ir.OpRotateRight,
			ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy,
			ir.OpVInsert:
			return true
		case ir.OpMul:
			return true
		}
	}
	switch op {
	case ir.OpVLoad, ir.OpVStore, ir.OpVSplat, ir.OpVConst, ir.OpVExtract, ir.OpVZero, ir.OpVInsertZero, ir.OpAdd, ir.OpSub, ir.OpAnd, ir.OpOr, ir.OpXor:
		return true
	case ir.OpShiftLeft, ir.OpShiftRight, ir.OpRotateLeft, ir.OpRotateRight:
		return t.LaneBits == 32 && ((arch == "amd64" || arch == "386") || arch == "arm64")
	case ir.OpVCarrylessMul:
		return (arch == "amd64" || arch == "386") && t.LaneBits == 64 && t.Lanes%2 == 0 && slices.Contains(features, "pclmulqdq")
	case ir.OpMul:
		return (arch == "amd64" || arch == "386") && t.LaneBits == 16 || arch == "arm64" && t.LaneBits <= 32
	case ir.OpVInsert:
		return arch == "arm64" || t.LaneBits == 16
	case ir.OpVCompare:
		return t.LaneBits == 8
	}
	return false
}
func validateVectorFeatures(options compileOptions) error {
	arch := options.Architecture
	if arch == "" {
		arch = runtime.GOARCH
	}
	for _, f := range options.Features {
		if !((f == "sse2" || f == "pclmulqdq") && (arch == "amd64" || arch == "386") || f == "neon" && arch == "arm64" || (f == "lsx" || f == "lasx") && arch == "loong64") {
			return fmt.Errorf("SIMD feature %q is unavailable for %s: %w", f, arch, ErrBackendLimit)
		}
	}
	if arch == "loong64" {
		_, err := resolveCPUProfile(options)
		return err
	}
	return nil
}
