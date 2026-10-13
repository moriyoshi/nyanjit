package compile_test

import (
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/goregister"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

var stackArchitectures = []string{"amd64", "arm64", "386", "riscv64", "ppc64le", "loong64"}

var stackTiers = []compile.Tier{compile.TierOptimized, compile.TierBaseline}

// stackProgram returns a raw program for pointer width bits with one area of
// words pointer-sized words and alignment align. It reads word 0, stores its
// argument to the last word, and returns the old word 0 plus the stored word
// loaded back: its argument, since the area is zeroed at entry.
func stackProgram(t testing.TB, bits uint8, words, align int) *ir.SealedProgram {
	t.Helper()
	p := &ir.Program{Arity: 1, StackAreas: []ir.StackArea{{Bytes: words * int(bits/8), Align: align}}}
	add := func(ins ir.Instruction) int {
		p.Instructions = append(p.Instructions, ins)
		return len(p.Instructions) - 1
	}
	load, store := ir.OpLoad64, ir.OpStore64
	if bits == 32 {
		load, store = ir.OpLoad32, ir.OpStore32
	}
	x := add(ir.Instruction{Op: ir.OpParam, Bits: bits})
	base := add(ir.Instruction{Op: ir.OpStackAddress, Bits: bits})
	old := add(ir.Instruction{Op: load, Bits: bits, A: base, Aligned: bits == 32})
	last := add(ir.Instruction{Op: ir.OpAdd, Bits: bits, A: base, B: add(ir.Instruction{Op: ir.OpConst, Bits: bits, Imm: uint64((words - 1) * int(bits/8))})})
	add(ir.Instruction{Op: store, Bits: bits, A: last, B: x, Aligned: bits == 32})
	loaded := add(ir.Instruction{Op: load, Bits: bits, A: last, Aligned: bits == 32})
	p.Result = add(ir.Instruction{Op: ir.OpAdd, Bits: bits, A: old, B: loaded})
	sealed, err := ir.SealOwned(p)
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func pointerBits(arch string) uint8 {
	if arch == "386" {
		return 32
	}
	return 64
}

// Areas live in the frame with a platform stack and in the workspace
// without one, on every target and tier; native code reads them as zero on
// every entry, although the previous call wrote them.
func TestStackAreaPlacement(t *testing.T) {
	for _, arch := range stackArchitectures {
		for _, tier := range stackTiers {
			bits := pointerBits(arch)
			words := 6
			p := stackProgram(t, bits, words, int(bits/8))
			for _, f := range []compile.Func{
				{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}},
				{ABI: abi.C},
			} {
				name := fmt.Sprintf("%s/tier%d/stack%d", arch, tier, f.Stack.Practice)
				u, err := lowerUnit(p, compile.TargetSpec{Architecture: arch, Tier: tier}, f)
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				r, need := u.Report(), words*int(bits/8)
				if f.Stack.Practice == abi.StackPlatform {
					if r.StackBytes() < need || r.WorkspaceBytes() != 0 {
						t.Errorf("%s: %d stack bytes, %d workspace bytes; want at least %d on the stack", name, r.StackBytes(), r.WorkspaceBytes(), need)
					}
				} else if r.WorkspaceBytes() < need || r.StackBytes() != 0 || r.SpillStorage() != abi.SpillWorkspace {
					t.Errorf("%s: %d workspace bytes (%v), %d stack bytes; want at least %d in the workspace", name, r.WorkspaceBytes(), r.SpillStorage(), r.StackBytes(), need)
				}
				if _, err := u.Listing(); err != nil {
					t.Errorf("%s: listing: %v", name, err)
				}
				if arch != runtime.GOARCH {
					continue
				}
				template, err := u.Template()
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				code, err := template.Bind(nil)
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				for _, x := range []uintptr{0x5a5a, 7, 0x1234} {
					if got, err := code.Call(x); err != nil || got != x {
						t.Errorf("%s(%#x) = %#x, %v; the area was not zeroed at entry", name, x, got, err)
					}
				}
				code.Close()
			}
		}
	}
}

// Areas count toward the spill words: beyond them Lower returns a
// *LimitError at both tiers.
func TestStackAreaSpillWordsLimit(t *testing.T) {
	for _, arch := range stackArchitectures {
		for _, tier := range stackTiers {
			bits := pointerBits(arch)
			spec := compile.TargetSpec{Architecture: arch, Tier: tier}
			if _, err := lowerUnit(stackProgram(t, bits, 3000, int(bits/8)), spec, compile.Func{ABI: abi.C}); err != nil {
				t.Errorf("%s/%v: 3000 words: %v", arch, tier, err)
			}
			_, err := lowerUnit(stackProgram(t, bits, 4097, int(bits/8)), spec, compile.Func{ABI: abi.C})
			var limit *compile.LimitError
			if !errors.As(err, &limit) || limit.Kind != compile.LimitSpillWords || limit.Needed <= limit.Limit {
				t.Errorf("%s/%v: 4097 words: %v", arch, tier, err)
			}
		}
	}
}

// A platform-stack frame holds the areas within BytesAvailable.
func TestStackAreaBytesAvailable(t *testing.T) {
	for _, arch := range stackArchitectures {
		for _, tier := range stackTiers {
			bits := pointerBits(arch)
			f := compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 256}}
			_, err := lowerUnit(stackProgram(t, bits, 64, int(bits/8)), compile.TargetSpec{Architecture: arch, Tier: tier}, f)
			if !errors.Is(err, compile.ErrStackRequired) {
				t.Errorf("%s/%v: %d-byte area in a 256-byte budget: %v", arch, tier, 64*bits/8, err)
			}
		}
	}
}

// A Go-managed frame holds the areas within the managed-frame limit (640
// bytes on 64-bit targets, 240 on 386): Template returns a *LimitError
// beyond it, never a crash, and the frames below it run.
func TestStackAreaManagedFrameLimit(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("runtime module layout is unsupported")
	}
	bits := pointerBits(runtime.GOARCH)
	for _, tier := range stackTiers {
		for _, abiFlavor := range []abi.Flavor{abi.Go, abi.C} {
			ran, limited := 0, 0
			for _, words := range []int{1, 8, 32, 60, 100, 200} {
				f := compile.Func{ABI: abiFlavor, Context: compile.ContextGoManaged, AssumePointerFree: true,
					Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 14}}
				u, err := lowerUnit(stackProgram(t, bits, words, int(bits/8)), compile.TargetSpec{Tier: tier}, f)
				if err != nil {
					t.Fatalf("%v/%v/%d words: %v", tier, abiFlavor, words, err)
				}
				template, err := u.Template()
				var limit *compile.LimitError
				switch {
				case errors.As(err, &limit):
					if limit.Kind != compile.LimitFrameBytes || limit.Needed <= limit.Limit || limit.Needed < words*int(bits/8) {
						t.Errorf("%v/%v/%d words: %v", tier, abiFlavor, words, err)
					}
					limited++
					continue
				case err != nil:
					t.Fatalf("%v/%v/%d words: %v", tier, abiFlavor, words, err)
				case words*int(bits/8) > 640:
					t.Fatalf("%v/%v/%d words: a frame above the managed limit was accepted", tier, abiFlavor, words)
				}
				code, err := template.Bind(nil)
				if err != nil {
					t.Fatalf("%v/%v/%d words: %v", tier, abiFlavor, words, err)
				}
				for _, x := range []uintptr{3, 0x77} {
					var got uintptr
					if abiFlavor == abi.Go && bits == 32 {
						// A 386 Go-ABI entry writes an 8-byte result.
						var fn func(uint32) uint64
						if err := code.Func(&fn); err != nil {
							t.Fatal(err)
						}
						got = uintptr(uint32(fn(uint32(x))))
					} else if abiFlavor == abi.Go {
						var fn func(uint64) uint64
						if err := code.Func(&fn); err != nil {
							t.Fatal(err)
						}
						got = uintptr(fn(uint64(x)))
					} else if got, err = code.Call(x); err != nil {
						t.Fatal(err)
					}
					if got != x {
						t.Errorf("%v/%v/%d words: f(%#x) = %#x", tier, abiFlavor, words, x, got)
					}
				}
				code.Close()
				ran++
			}
			// ppc64le's managed C frames may leave no room for an area.
			if ran == 0 && !(runtime.GOARCH == "ppc64le" && abiFlavor == abi.C) || limited == 0 {
				t.Errorf("%v/%v: %d frames ran, %d over the limit", tier, abiFlavor, ran, limited)
			}
		}
	}
}

// A Go-managed function with stack areas cannot make calls: a call may
// move the goroutine stack under the areas' addresses.
func TestStackAreaManagedCallsUnsupported(t *testing.T) {
	if builder.NativeBits != 64 {
		t.Skip("cross-target fixture uses 64-bit builder addresses")
	}
	for _, withArea := range []bool{false, true} {
		p := finished(t, func(b *nyanjit.Builder) {
			x := b.Param()
			if withArea {
				nyanjit.StackData[uint64](b).Store(x)
			}
			b.Return(b.CallC(b.Symbol("callee"), x))
		})
		for _, arch := range []string{"amd64", "arm64", "riscv64", "ppc64le", "loong64"} {
			for _, tier := range stackTiers {
				f := compile.Func{ABI: abi.Go, Context: compile.ContextGoManaged, AssumePointerFree: true,
					Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}}
				_, err := lowerUnit(p, compile.TargetSpec{Architecture: arch, Tier: tier}, f)
				if withArea && (!errors.Is(err, compile.ErrUnsupported) || !strings.Contains(err.Error(), "stack areas")) {
					t.Errorf("%s/%v: managed call with a stack area: %v", arch, tier, err)
				}
				if !withArea && err != nil && strings.Contains(err.Error(), "stack areas") {
					t.Errorf("%s/%v: managed call without stack areas: %v", arch, tier, err)
				}
			}
		}
	}
}

// 386 frames align areas to 4 bytes at most; 64-bit targets to 16. An
// address narrower than a pointer is a backend limit.
func TestStackAreaAlignmentAndWidthLimits(t *testing.T) {
	for _, tier := range stackTiers {
		for _, c := range []struct {
			arch  string
			align int
			ok    bool
		}{
			{"386", 4, true}, {"386", 8, false}, {"386", 16, false},
			{"amd64", 16, true}, {"arm64", 16, true}, {"riscv64", 16, true}, {"ppc64le", 16, true}, {"loong64", 16, true},
		} {
			for _, stack := range []abi.StackPractice{abi.StackPlatform, abi.StackNoUse} {
				f := compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: stack}}
				if stack == abi.StackPlatform {
					f.Stack.BytesAvailable = 4096
				}
				_, err := lowerUnit(stackProgram(t, pointerBits(c.arch), 3, c.align), compile.TargetSpec{Architecture: c.arch, Tier: tier}, f)
				if c.ok && err != nil || !c.ok && !errors.Is(err, compile.ErrBackendLimit) {
					t.Errorf("%s/%v/%v: alignment %d: %v", c.arch, tier, stack, c.align, err)
				}
			}
		}
		_, err := lowerUnit(stackProgram(t, 32, 3, 4), compile.TargetSpec{Architecture: "arm64", Tier: tier}, compile.Func{ABI: abi.C})
		if !errors.Is(err, compile.ErrBackendLimit) {
			t.Errorf("%v: 32-bit stack address on arm64: %v", tier, err)
		}
	}
}

// A 16-byte aligned area's address is rounded up at run time.
func TestStackAreaAlignedAddress(t *testing.T) {
	if runtime.GOARCH == "386" {
		t.Skip("386 frames align areas to 4 bytes")
	}
	p := &ir.Program{Arity: 1, StackAreas: []ir.StackArea{{Bytes: 1, Align: 1}, {Bytes: 32, Align: 16}, {Bytes: 8, Align: 8}}}
	p.Instructions = []ir.Instruction{
		{Op: ir.OpParam},
		{Op: ir.OpStackAddress, Imm: 1},
		{Op: ir.OpConst, Imm: 15},
		{Op: ir.OpAnd, A: 1, B: 2},
		{Op: ir.OpStore64, A: 1, B: 0},
		{Op: ir.OpLoad64, A: 1},
		{Op: ir.OpAdd, A: 3, B: 5},
	}
	p.Result = 6
	for _, tier := range stackTiers {
		for _, f := range []compile.Func{{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}}, {ABI: abi.C}} {
			template, err := lowerTemplate(p, compile.TargetSpec{Tier: tier}, f)
			if err != nil {
				t.Fatal(err)
			}
			code, err := template.Bind(nil)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := code.Call(0x40); err != nil || got != 0x40 {
				t.Errorf("%v/%v: misaligned 16-byte area: %#x, %v", tier, f.Stack.Practice, got, err)
			}
			code.Close()
		}
	}
}

// Hardened code clamps a checked index into a stack area on its success
// edge, at both tiers, as it does for every Data index.
func TestStackAreaHardenClamp(t *testing.T) {
	if builder.NativeBits != 64 {
		t.Skip("cross-target fixture uses 64-bit builder addresses")
	}
	p := finished(t, func(b *nyanjit.Builder) {
		i := b.Param()
		array := nyanjit.StackData[[8]uint64](b)
		bad := b.NewLabel()
		array.Index(i, bad).Store(i)
		b.Return(array.Field("[0]"))
		b.Bind(bad)
		b.Return(b.Const(0))
	})
	f := compile.Func{Context: compile.ContextExternal, Harden: true, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}}
	for _, c := range []struct{ arch, clamp string }{
		{"amd64", `\.byte 4[89a-f] 0f 43 `}, // CMOVAE r64, r64
		{"arm64", `csel`},
		{"ppc64le", `isel r\d+, r\d+, r\d+, 0`},
		{"riscv64", ""},
		{"loong64", ""},
	} {
		for _, tier := range stackTiers {
			listing, err := lowerListing(p, compile.TargetSpec{Architecture: c.arch, Tier: tier}, f)
			if c.clamp == "" {
				if !errors.Is(err, compile.ErrUnsupported) {
					t.Errorf("%s/%v: hardened control flow accepted without a speculation barrier: %v", c.arch, tier, err)
				}
				continue
			}
			if err != nil {
				t.Fatalf("%s/%v: %v", c.arch, tier, err)
			}
			if !regexp.MustCompile(c.clamp).MatchString(listing) {
				t.Errorf("%s/%v: no index clamp:\n%s", c.arch, tier, listing)
			}
		}
	}
}
