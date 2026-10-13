//go:build linux && (loong64 || riscv64)

package compile_test

import (
	"errors"
	"math"
	"reflect"
	"runtime"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

//go:noinline
func lp64FrameGrow(depth int) {
	var buf [8192]byte
	buf[depth] = byte(depth)
	if depth != 0 {
		lp64FrameGrow(depth - 1)
	}
	runtime.KeepAlive(buf)
}

//go:noinline
func lp64FrameCallee(a uint64) uint64 {
	lp64FrameGrow(8)
	runtime.GC()
	return a ^ 0x9876543210fedcba
}

func lp64PressureProgram(t *testing.T, count int) (*ir.Program, uint64) {
	t.Helper()
	b := n.NewBuilder()
	x := b.Param()
	input := math.Float64bits(2.5)
	var ints, floats []n.Value
	var floatBits []uint64
	var want uint64
	iv, fv := x, x
	ip, fp := input, 2.5
	for i := 0; i < count; i++ {
		iv = b.Add(b.Xor(iv, x), b.Const(uint64(i+9)))
		ints = append(ints, iv)
		ip = (ip ^ input) + uint64(i+9)
		want += ip
	}
	for i := 0; i < 14; i++ {
		fv = b.FMul(b.FAdd(fv, b.Float64(float64(i)+0.25)), b.Float64(0.5))
		floats = append(floats, fv)
		fp = (fp + float64(i) + 0.25) * 0.5
		floatBits = append(floatBits, math.Float64bits(fp))
	}
	var calls []n.Value
	arg := b.Xor(iv, fv)
	argBits := ip ^ math.Float64bits(fp)
	for i := 0; i < 3; i++ {
		calls = append(calls, b.CallGo(b.ConstNative(reflect.ValueOf(lp64FrameCallee).Pointer()), b.Add(arg, b.Const(uint64(i)))))
		want += (argBits + uint64(i)) ^ 0x9876543210fedcba
	}
	for _, bits := range floatBits {
		want ^= bits
	}
	sum := calls[0]
	for _, v := range calls[1:] {
		sum = b.Add(sum, v)
	}
	for i := len(ints) - 1; i >= 0; i-- {
		sum = b.Add(sum, ints[i])
	}
	for i := len(floats) - 1; i >= 0; i-- {
		sum = b.Xor(sum, floats[i])
	}
	b.Return(sum)
	p, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	return p, want
}

func TestLP64ManagedFrameAtLimit(t *testing.T) {
	fn := compile.Func{ABI: abi.C, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}, AssumePointerFree: true}
	foundLimit, foundAbove := false, false
	for count := 1; count <= 80; count++ {
		p, want := lp64PressureProgram(t, count)
		// The lowering report gives the frame size without the managed-frame
		// limit check that encoding a template applies.
		report, err := lowerReport(p, compile.TargetSpec{}, fn)
		if err != nil {
			t.Fatal(err)
		}
		size := report.StackBytes()
		t.Logf("integers=%d frame=%d", count, size)
		if size < 640 {
			continue
		}
		template, err := lowerTemplate(p, compile.TargetSpec{}, fn)
		if size > 640 {
			if !errors.Is(err, compile.ErrBackendLimit) {
				t.Fatalf("frame %d: %v, want ErrBackendLimit", size, err)
			}
			foundAbove = true
			break
		}
		if err != nil {
			t.Fatalf("frame %d: %v", size, err)
		}
		c, err := template.Bind(nil)
		if err != nil {
			t.Fatal(err)
		}
		got, err := c.Call(execution.Words(uintptr(math.Float64bits(2.5)))...)
		c.Close()
		if err != nil || uint64(got) != want {
			t.Fatalf("frame %d got %#x,%v want %#x", size, got, err, want)
		}
		t.Logf("640-byte frame with %d live integers and 14 FP values survives three Go calls, GC and stack growth", count)
		foundLimit = true
	}
	if !foundLimit || !foundAbove {
		t.Fatalf("missing exact boundary coverage: limit=%v above=%v", foundLimit, foundAbove)
	}
}

func TestLP64ManagedLeafAvoidsCallSaveSet(t *testing.T) {
	b := n.NewBuilder()
	b.Return(b.Add(b.Param(), b.Const(1)))
	p, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	template, err := lowerTemplate(p, compile.TargetSpec{}, compile.Func{ABI: abi.C, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}, AssumePointerFree: true})
	if err != nil {
		t.Fatal(err)
	}
	if size := template.Report().StackBytes(); size != 0 {
		t.Fatalf("leaf needs no frame, got %d bytes", size)
	}
	c, err := template.Bind(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if got, err := c.Call(execution.Words(41)...); err != nil || got != 42 {
		t.Fatalf("got%d,%v", got, err)
	}
}
