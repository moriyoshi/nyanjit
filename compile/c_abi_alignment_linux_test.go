//go:build linux && (loong64 || riscv64)

package compile_test

import (
	"embed"
	"encoding/binary"
	"encoding/hex"
	"runtime"
	"slices"
	"strings"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
)

// The independent C leaf returns SP%16 in a0 and the eighth argument in a1.
// Neither architecture allows the caller to bias SP by a return-address word.
func alignmentCallee(t *testing.T) uintptr {
	t.Helper()
	words := []uint32{0x03403c64, 0x00150165, 0x4c000020} // andi r4,r3,15; or r5,r11,r0; ret
	if runtime.GOARCH == "riscv64" {
		words = []uint32{0x00f17513, 0x00088593, 0x00008067}
	} // andi a0,sp,15; mv a1,a7; ret
	raw := make([]byte, 4*len(words))
	for i, w := range words {
		binary.LittleEndian.PutUint32(raw[i*4:], w)
	}
	c, err := execution.NewCode(raw, execution.CodeOptions{ABI: abi.C}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	entry, err := c.EntryAddress()
	if err != nil {
		t.Fatal(err)
	}
	return entry
}
func TestCABIStackAlignment(t *testing.T) {
	entry := alignmentCallee(t)
	for _, count := range []int{0, 1, 7, 8} {
		args := make([]uintptr, count)
		for i := range args {
			args[i] = uintptr(i + 101)
		}
		lo, hi := execution.CallForeignPair(entry, args)
		wantHi := uintptr(0)
		if count == 8 {
			wantHi = args[7]
		}
		if lo != 0 || hi != wantHi {
			t.Errorf("CallForeignPair(%d args) SP%%16=%d high=%x want%x", count, lo, hi, wantHi)
		}
	}
	for _, mode := range []string{"foreign", "managed-C-entry", "managed-Go-entry"} {
		for _, pressure := range []int{0, 3, 19} {
			b := n.NewBuilder()
			x := b.Param()
			values := make([]n.Value, pressure)
			for i := range values {
				values[i] = b.Add(x, b.Const(uint64(i+1)))
			}
			args := make([]n.Value, 8)
			for i := range args {
				args[i] = b.Const(uint64(i + 11))
			}
			result := b.CallC(b.ConstNative(entry), args...)
			for _, v := range values {
				result = b.Add(result, b.And(v, b.Const(0)))
			}
			b.Return(result)
			p, err := testFinishRaw(b)
			if err != nil {
				t.Fatal(err)
			}
			fn := compile.Func{ABI: abi.C, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}, AssumePointerFree: true}
			if mode == "foreign" {
				fn.Context = compile.ContextForeign
			} else if mode == "managed-Go-entry" {
				fn.ABI = abi.Go
			}
			var c *execution.Code
			c, err = n.Compile(p, compile.TargetSpec{}, fn)
			if err != nil {
				t.Fatalf("%s pressure%d: %v", mode, pressure, err)
			}
			for attempt := 0; attempt < 3; attempt++ {
				got, err := c.Call(execution.Words(55)...)
				if err != nil || got != 0 {
					t.Errorf("%s pressure%d SP%%16=%d err%v", mode, pressure, got, err)
				}
			}
			c.Close()
		}
	}
}

func TestLP64CArgumentExtension(t *testing.T) {
	word := uint32(0x4c000020)
	if runtime.GOARCH == "riscv64" {
		word = 0x00008067
	}
	raw := make([]byte, 4)
	binary.LittleEndian.PutUint32(raw, word)
	callee, err := execution.NewCode(raw, execution.CodeOptions{ABI: abi.C}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer callee.Close()
	entry, err := callee.EntryAddress()
	if err != nil {
		t.Fatal(err)
	}
	for _, managed := range []bool{false, true} {
		for _, w := range []uint8{32, 64} {
			b := n.NewBuilder()
			x := b.ParamBits(w)
			b.Return(b.CallC(b.ConstNative(entry), x))
			p, err := testFinishRaw(b)
			if err != nil {
				t.Fatal(err)
			}
			fn := compile.Func{Context: compile.ContextForeign, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 512}, AssumePointerFree: true}
			if managed {
				fn.Context = compile.ContextGoManaged
			}
			var c *execution.Code
			c, err = n.Compile(p, compile.TargetSpec{}, fn)
			if err != nil {
				t.Fatal(err)
			}
			for _, x := range []uint64{0, 1, 0x7fffffff, 0x80000000, 0xffffffff} {
				want := x
				if w == 32 {
					want = uint64(int64(int32(x)))
				}
				got, err := c.Call(execution.Words(uintptr(x))...)
				if err != nil || uint64(got) != want {
					t.Errorf("managed%v width%d arg%x register=%x,%v want%x", managed, w, x, got, err, want)
				}
			}
			c.Close()
		}
	}
}

func TestGoEntryReportsPreservedRegisterWrites(t *testing.T) {
	mem, err := execution.Scratch(24 * 8)
	if err != nil {
		t.Fatal(err)
	}
	defer execution.FreeScratch(mem)
	for i := 0; i < 24; i++ {
		binary.LittleEndian.PutUint64(mem[i*8:], uint64(100+i))
	}
	for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
		b := n.NewBuilder()
		ptr := b.Var(b.ParamNative())
		acc, count := b.Var(b.Const(0)), b.Var(b.Const(1))
		loop, done := b.NewLabel(), b.NewLabel()
		b.Jump(loop)
		b.Bind(loop)
		vs := make([]n.Value, 24)
		for i := range vs {
			vs[i] = b.Add(b.Load64(b.Add(b.Get(ptr), b.ConstNative(uintptr(i*8)))), b.Const(uint64(i)))
		}
		sum := vs[0]
		for _, v := range vs[1:] {
			sum = b.Add(sum, v)
		}
		b.Set(acc, sum)
		b.Set(count, b.Sub(b.Get(count), b.Const(1)))
		b.Branch(b.IsZero(b.Get(count)), done)
		b.Jump(loop)
		b.Bind(done)
		b.Return(b.Get(acc))
		p, err := testFinishRaw(b)
		if err != nil {
			t.Fatal(err)
		}
		template, err := lowerTemplate(p, compile.TargetSpec{}, compile.Func{ABI: flavor, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}, AssumePointerFree: true})
		if err != nil {
			t.Fatal(err)
		}
		names := []string{"R24", "R25", "R26", "R27", "R28", "R31"}
		if runtime.GOARCH == "riscv64" {
			names = []string{"X18", "X19", "X20", "X21", "X22", "X23", "X24", "X25", "X26"}
		}
		found := false
		for _, name := range names {
			if slices.Contains(template.Report().ClobberedRegisters(), name) {
				found = true
			}
		}
		if found != (flavor == abi.Go) {
			t.Fatalf("ABI%d report hides writes or exposes saved registers: %v", flavor, template.Report().ClobberedRegisters())
		}
		c, err := template.Bind(nil)
		if err != nil {
			t.Fatal(err)
		}
		got, err := c.Call(execution.Words(&mem[0])...)
		c.Close()
		if err != nil || got != 2952 {
			t.Fatalf("ABI%d got%d,%v want2952", flavor, got, err)
		}
	}
}

//go:embed testdata/cabi/*.hex
var cABIAlignmentFixtures embed.FS

func TestCABIAlignedInt128Local(t *testing.T) {
	data, err := cABIAlignmentFixtures.ReadFile("testdata/cabi/aligned_local_" + runtime.GOARCH + ".hex")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := hex.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	callee, err := execution.NewCode(raw, execution.CodeOptions{ABI: abi.C}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer callee.Close()
	entry, err := callee.EntryAddress()
	if err != nil {
		t.Fatal(err)
	}
	seeds := []uintptr{0, 7, ^uintptr(0)}
	for _, seed := range seeds {
		got := execution.CallForeign(entry, []uintptr{seed})
		if got != 0 {
			t.Errorf("direct seed%x: aligned C local check=%x", seed, got)
		}
	}
	for _, mode := range []string{"foreign", "managed-C-entry", "managed-Go-entry"} {
		t.Run(mode, func(t *testing.T) {
			b := n.NewBuilder()
			arg := b.Param()
			b.Return(b.CallC(b.ConstNative(entry), arg))
			p, err := testFinishRaw(b)
			if err != nil {
				t.Fatal(err)
			}
			fn := compile.Func{ABI: abi.C, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 512}, AssumePointerFree: true}
			if mode == "foreign" {
				fn.Context = compile.ContextForeign
			} else if mode == "managed-Go-entry" {
				fn.ABI = abi.Go
			}
			var c *execution.Code
			c, err = n.Compile(p, compile.TargetSpec{}, fn)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, seed := range seeds {
				got, err := c.Call(execution.Words(seed)...)
				if err != nil || got != 0 {
					t.Errorf("seed%x: aligned C local check=%x, %v", seed, got, err)
				}
			}
		})
	}
}
