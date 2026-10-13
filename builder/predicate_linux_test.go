//go:build linux && (arm64 || amd64 || riscv64)

package builder

import (
	"math"
	"strconv"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

var publicBranchConditions = []struct {
	name      string
	condition ir.BranchCondition
}{
	{"equal", ir.BranchEqual},
	{"not equal", ir.BranchNotEqual},
	{"signed less", ir.BranchLess},
	{"signed greater or equal", ir.BranchGreaterEqual},
	{"signed less or equal", ir.BranchLessEqual},
	{"signed greater", ir.BranchGreater},
	{"unsigned below", ir.BranchBelow},
	{"unsigned above or equal", ir.BranchAboveEqual},
	{"unsigned below or equal", ir.BranchBelowEqual},
	{"unsigned above", ir.BranchAbove},
}

func comparisonResult(left, right uint64, condition ir.BranchCondition) uint64 {
	var matches bool
	switch condition {
	case ir.BranchEqual:
		matches = left == right
	case ir.BranchNotEqual:
		matches = left != right
	case ir.BranchLess:
		matches = int64(left) < int64(right)
	case ir.BranchGreaterEqual:
		matches = int64(left) >= int64(right)
	case ir.BranchLessEqual:
		matches = int64(left) <= int64(right)
	case ir.BranchGreater:
		matches = int64(left) > int64(right)
	case ir.BranchBelow:
		matches = left < right
	case ir.BranchAboveEqual:
		matches = left >= right
	case ir.BranchBelowEqual:
		matches = left <= right
	case ir.BranchAbove:
		matches = left > right
	default:
		panic("unsupported test condition")
	}
	if matches {
		return 1
	}
	return 0
}

func compileBooleanBranch(t *testing.T, build func(*Builder, Value, Value) (Predicate, error), parameters int) *Code {
	t.Helper()
	b := NewBuilder()
	entry, yes, no := b.NewLabel(), b.NewLabel(), b.NewLabel()
	requireControlOK(t, b.Bind(entry))
	left, err := b.Param()
	requireControlOK(t, err)
	var right Value
	if parameters == 2 {
		right, err = b.Param()
		requireControlOK(t, err)
	}
	predicate, err := build(b, left, right)
	requireControlOK(t, err)
	requireControlOK(t, b.Branch(predicate, yes))
	requireControlOK(t, b.Jump(no))
	requireControlOK(t, b.Bind(yes))
	requireControlOK(t, b.Return(controlConst(t, b, 1)))
	requireControlOK(t, b.Bind(no))
	requireControlOK(t, b.Return(controlConst(t, b, 0)))
	code, err := testCompileWithOptions(b, controlOptions())
	requireControlOK(t, err)
	return code
}

func TestCompareValueBranchConditions(t *testing.T) {
	values := []uint64{0, 1, 2, uint64(1) << 63, math.MaxUint64 - 1, math.MaxUint64}
	for _, tc := range publicBranchConditions {
		t.Run(tc.name, func(t *testing.T) {
			code := compileBooleanBranch(t, func(b *Builder, left, right Value) (Predicate, error) {
				return b.Compare(left, tc.condition, right)
			}, 2)
			defer code.Close()
			for _, left := range values {
				for _, right := range values {
					got, err := code.Call(uintptr(left), uintptr(right))
					requireControlOK(t, err)
					if want := comparisonResult(left, right, tc.condition); uint64(got) != want {
						t.Fatalf("Compare(%#x, %d, %#x) = %d, want %d", left, tc.condition, right, got, want)
					}
				}
			}
		})
	}
}

func TestCompareImmediateBranchConditions(t *testing.T) {
	values := []uint64{0, 1, 4095, 4096, uint64(1) << 63, math.MaxUint64}
	for _, immediate := range []int64{-1, 4096, math.MinInt64} {
		for _, tc := range publicBranchConditions {
			t.Run(tc.name+"/immediate="+strconv.FormatInt(immediate, 10), func(t *testing.T) {
				code := compileBooleanBranch(t, func(b *Builder, left, _ Value) (Predicate, error) {
					return b.CompareImmediate(left, tc.condition, immediate)
				}, 1)
				defer code.Close()
				for _, left := range values {
					got, err := code.Call(uintptr(left))
					requireControlOK(t, err)
					if want := comparisonResult(left, uint64(immediate), tc.condition); uint64(got) != want {
						t.Fatalf("CompareImmediate(%#x, %d, %d) = %d, want %d", left, tc.condition, immediate, got, want)
					}
				}
			})
		}
	}
}

func TestCarryPredicates(t *testing.T) {
	values := []uint64{0, 1, 2, uint64(1) << 63, math.MaxUint64}
	for _, operation := range []struct {
		name  string
		build func(*Builder, Value, Value) (Value, error)
		want  func(uint64, uint64) uint64
	}{
		{name: "add overflow", build: (*Builder).Add, want: func(left, right uint64) uint64 {
			return comparisonResult(left+right, left, ir.BranchBelow)
		}},
		{name: "sub borrow", build: (*Builder).Sub, want: func(left, right uint64) uint64 {
			return comparisonResult(left, right, ir.BranchBelow)
		}},
	} {
		t.Run(operation.name, func(t *testing.T) {
			code := compileBooleanBranch(t, func(b *Builder, left, right Value) (Predicate, error) {
				result, err := operation.build(b, left, right)
				if err != nil {
					return Predicate{}, err
				}
				return b.Carry(result)
			}, 2)
			defer code.Close()
			for _, left := range values {
				for _, right := range values {
					got, err := code.Call(uintptr(left), uintptr(right))
					requireControlOK(t, err)
					if want := operation.want(left, right); uint64(got) != want {
						t.Fatalf("Carry(%#x, %#x) = %d, want %d", left, right, got, want)
					}
				}
			}
		})
	}
}

func TestCarryAfterDecrement(t *testing.T) {
	code := compileBooleanBranch(t, func(b *Builder, left, _ Value) (Predicate, error) {
		one := controlConst(t, b, 1)
		decremented, err := b.Sub(left, one)
		if err != nil {
			return Predicate{}, err
		}
		return b.Carry(decremented)
	}, 1)
	defer code.Close()
	for _, input := range []uint64{0, 1, 2, math.MaxUint64} {
		got, err := code.Call(uintptr(input))
		requireControlOK(t, err)
		want := uint64(0)
		if input == 0 {
			want = 1
		}
		if uint64(got) != want {
			t.Fatalf("Carry(%#x - 1) = %d, want %d", input, got, want)
		}
	}
}
