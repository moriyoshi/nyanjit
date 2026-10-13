//go:build linux && 386

package compile_test

import (
	"encoding/binary"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/execution"
	"github.com/moriyoshi/nyanjit/ir"
)

func cdeclProgram(t *testing.T, b *nyanjit.Builder) *ir.Program {
	t.Helper()
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func Test386CdeclMixedEntry(t *testing.T) {
	for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
		b := nyanjit.NewBuilder()
		a, wide, c := b.ParamNative(), b.ParamBits(64), b.ParamBits(32)
		small, tiny := b.ParamBits(16), b.ParamBits(8)
		b.ParamBits(64) // An unused parameter still occupies two ABI words.
		last := b.ParamNative()
		sum := b.Add(b.CastUnsigned(a, 64), wide)
		for _, v := range []nyanjit.Value{c, small, tiny, last} {
			sum = b.Add(sum, b.CastUnsigned(v, 64))
		}
		b.Return(sum)
		p := cdeclProgram(t, b)
		options := abi.CompileOptions{ABI: flavor, Stack: abi.StackPlatform, StackBytesAvailable: 640}
		report, err := compile.Analyze(p, options)
		if err != nil {
			t.Fatal(err)
		}
		if got := report.ArgumentLocations; len(got) != 7 || len(got[0].Words) != 1 || len(got[1].Words) != 2 || got[6].Words[0].StackOffset != 36 {
			t.Fatalf("entry locations: %+v", got)
		}
		code, err := compile.Compile(p, options)
		if err != nil {
			t.Fatal(err)
		}
		lo, hi, err := code.CallPair(execution.Words("lLlllLl", 3, 0x1234567800000010, 5, 0x10007, 0x109, 0xffffffffffffffff, 11)...)
		code.Close()
		want := uint64(0x1234567800000010 + 3 + 5 + 7 + 9 + 11)
		if err != nil || execution.Wide(lo, hi) != want {
			t.Fatalf("ABI%d got%#x,%v want%#x", flavor, execution.Wide(lo, hi), err, want)
		}
	}
}

func Test386CdeclCallIntoWide(t *testing.T) {
	for _, flavor := range []abi.Flavor{abi.C} {
		b := nyanjit.NewBuilder()
		out, v := b.ParamNative(), b.ParamBits(64)
		b.Store64(out, v)
		b.Return(v)
		code, err := compile.Compile(cdeclProgram(t, b), abi.CompileOptions{ABI: flavor})
		if err != nil {
			t.Fatal(err)
		}
		output := make([]byte, 8)
		want := uint64(0xfedcba9876543210)
		_, err = code.CallInto(output, execution.Words("L", want)...)
		code.Close()
		if err != nil || binary.LittleEndian.Uint64(output) != want {
			t.Fatalf("ABI%d output%x,%v", flavor, output, err)
		}
	}
}

func Test386CdeclMixedOutgoing(t *testing.T) {
	for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
		options := abi.CompileOptions{ABI: flavor, Stack: abi.StackPlatform, StackBytesAvailable: 640, AssumePointerFree: true}
		callee := nyanjit.NewBuilder()
		a, w, c := callee.ParamNative(), callee.ParamBits(64), callee.ParamBits(32)
		callee.Return(callee.Add(callee.Add(callee.CastUnsigned(a, 64), w), callee.CastUnsigned(c, 64)))
		calleeCode, err := compile.Compile(cdeclProgram(t, callee), options)
		if err != nil {
			t.Fatal(err)
		}
		defer calleeCode.Close()
		address, err := calleeCode.EntryAddress()
		if err != nil {
			t.Fatal(err)
		}
		caller := nyanjit.NewBuilder()
		live := caller.ParamBits(64)
		var called nyanjit.Value
		if flavor == abi.C {
			called = caller.CallC(caller.ConstNative(address), caller.ConstNative(3), live, caller.ConstBits(32, 5))
		} else {
			called = caller.CallGo(caller.ConstNative(address), caller.ConstNative(3), live, caller.ConstBits(32, 5))
		}
		caller.Return(caller.Add(called, live))
		p := cdeclProgram(t, caller)
		report, err := compile.Analyze(p, options)
		if err != nil {
			t.Fatal(err)
		}
		// Four outgoing words (plus Go's result) fit with spills in a much smaller
		// frame than the former fixed 80-byte outgoing area.
		if report.StackBytes >= 100 || report.StackBytes%16 != 12 {
			t.Fatalf("call frame: %+v", report)
		}
		var code *compile.Code
		if flavor == abi.C {
			code, err = compile.CompileForeign(p, options)
		} else {
			code, err = compile.CompileGo(p, options)
		}
		if err != nil {
			t.Fatal(err)
		}
		want := uint64(0x1234567800000010)
		lo, hi, err := code.CallPair(execution.Words("L", want)...)
		code.Close()
		if err != nil || execution.Wide(lo, hi) != 2*want+8 {
			t.Fatalf("ABI%d result%#x,%v", flavor, execution.Wide(lo, hi), err)
		}
	}
}

//go:noinline
func cdeclGrow386(depth uint32, value uint64) uint64 {
	var frame [2048]uint32
	frame[0] = depth
	if depth != 0 {
		value = cdeclGrow386(depth-1, value)
	} else {
		runtime.GC()
	}
	runtime.KeepAlive(&frame)
	return value + uint64(frame[0])
}

func Test386CdeclManagedOddWords(t *testing.T) {
	b := nyanjit.NewBuilder()
	depth, value := b.ParamNative(), b.ParamBits(64)
	called := b.CallGo(b.ConstNative(reflect.ValueOf(cdeclGrow386).Pointer()), depth, value)
	b.Return(b.Add(called, value)) // Keep a paired value live across stack growth.
	p := cdeclProgram(t, b)
	code, err := compile.CompileGo(p, abi.CompileOptions{ABI: abi.Go, Stack: abi.StackPlatform, StackBytesAvailable: 640, AssumePointerFree: true})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	valueBits := uint64(0x1234567800000010)
	for i := 0; i < 3; i++ {
		lo, hi, err := code.CallPair(execution.Words("lL", 12, valueBits)...)
		if err != nil || execution.Wide(lo, hi) != 2*valueBits+78 {
			t.Fatalf("managed odd words result%#x,%v", execution.Wide(lo, hi), err)
		}
	}
}

func Test386CdeclMixedControlEntry(t *testing.T) {
	for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
		b := nyanjit.NewBuilder()
		a, w, c := b.ParamNative(), b.ParamBits(64), b.ParamNative()
		label := b.NewLabel()
		called := b.CallGo(b.ConstNative(reflect.ValueOf(cdeclGrow386).Pointer()), a, w)
		sum := b.Add(called, b.CastUnsigned(c, 64))
		b.JumpArgs(label, sum)
		ps := b.BindParamsBits(label, []uint8{64})
		b.Return(ps[0])
		p := cdeclProgram(t, b)
		options := abi.CompileOptions{ABI: flavor, Stack: abi.StackPlatform, StackBytesAvailable: 640, AssumePointerFree: true, ManagedCalls: true}
		report, err := compile.Analyze(p, options)
		if err != nil {
			t.Fatal(err)
		}
		if report.StackBytes%16 != 12 || report.StackBytes >= 100 {
			t.Fatalf("shared frame: %+v", report)
		}
		if len(report.ArgumentLocations) != 3 || report.ArgumentLocations[2].Words[0].StackOffset != 16 {
			t.Fatalf("control arguments: %+v", report.ArgumentLocations)
		}
		if flavor == abi.Go && report.ResultLocation.Words[0].StackOffset != 20 {
			t.Fatalf("control result: %+v", report.ResultLocation)
		}
		code, err := compile.CompileGo(p, options)
		if err != nil {
			t.Fatal(err)
		}
		v := uint64(0x1234567800000010)
		lo, hi, err := code.CallPair(execution.Words("lLl", 3, v, 5)...)
		code.Close()
		if err != nil || execution.Wide(lo, hi) != v+11 {
			t.Fatalf("control ABI%d result%#x,%v", flavor, execution.Wide(lo, hi), err)
		}
	}
}

func Test386CdeclManagedMixedCallback(t *testing.T) {
	address, err := execution.RegisterForeignCallback(func(a uint32, w uint64, c uint32) uint64 { runtime.GC(); return uint64(a) + w + uint64(c) })
	if err != nil {
		t.Fatal(err)
	}
	for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
		b := nyanjit.NewBuilder()
		target, a, w, c := b.ParamNative(), b.ParamNative(), b.ParamBits(64), b.ParamBits(32)
		b.Enter()
		called := b.CallC(target, a, w, c)
		b.Leave(called)
		b.Return(b.Add(called, w))
		p := cdeclProgram(t, b)
		options := abi.CompileOptions{ABI: flavor, Stack: abi.StackPlatform, StackBytesAvailable: 240, AssumePointerFree: true, ManagedCalls: true}
		if _, err := compile.PreflightRegisterScopes(p, options); err != nil {
			t.Fatal(err)
		}
		code, err := compile.CompileGo(p, options)
		if err != nil {
			t.Fatal(err)
		}
		value := uint64(0x1234567800000010)
		lo, hi, err := code.CallPair(execution.Words("llLl", uint64(address), 3, value, 5)...)
		code.Close()
		if err != nil || execution.Wide(lo, hi) != 2*value+8 {
			t.Fatalf("managed C ABI%d got%#x,%v", flavor, execution.Wide(lo, hi), err)
		}
	}
}

func Test386CdeclGoWorkspace(t *testing.T) {
	b := nyanjit.NewBuilder()
	a, w, c := b.ParamNative(), b.ParamBits(64), b.ParamNative()
	b.Return(b.Add(b.Add(b.CastUnsigned(a, 64), w), b.CastUnsigned(c, 64)))
	p := cdeclProgram(t, b)
	report, err := compile.Analyze(p, abi.CompileOptions{ABI: abi.Go})
	if err != nil {
		t.Fatal(err)
	}
	if report.WorkspaceBytes == 0 || report.StackBytes != 0 {
		t.Fatalf("test must require workspace spills: %+v", report)
	}
	code, err := compile.Compile(p, abi.CompileOptions{ABI: abi.Go})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	for _, wide := range []uint64{0, 0x1234567800000010, 0xfffffff0fffffffe} {
		args := execution.Words("lLl", 3, wide, 5)
		got, err := code.Call(args...)
		if err != nil || got != uintptr(wide+8) {
			t.Fatalf("Call with workspace got%#x,%v want%#x", got, err, uintptr(wide+8))
		}
		lo, hi, err := code.CallPair(args...)
		if err != nil || execution.Wide(lo, hi) != wide+8 {
			t.Fatalf("CallPair with workspace got%#x,%v", execution.Wide(lo, hi), err)
		}
	}
	if _, err := execution.Func[func(uintptr, uint64, uintptr) uint64](code); !errors.Is(err, execution.ErrInvalidCodeOptions) {
		t.Fatalf("direct func must still reject workspace: %v", err)
	}
}

//go:noinline
func cdeclGoSubwords(a uint8, b uint16, c uint8, d uint32) uint32 {
	return uint32(a) + uint32(b) + uint32(c) + d
}
func Test386CdeclGoSubwordsRejected(t *testing.T) {
	b := nyanjit.NewBuilder()
	called := b.CallGo(b.ConstNative(reflect.ValueOf(cdeclGoSubwords).Pointer()), b.ConstBits(8, 1), b.ConstBits(16, 2), b.ConstBits(8, 3), b.ConstBits(32, 4))
	b.Return(called)
	_, err := b.Finish()
	if !errors.Is(err, compile.ErrBackendLimit) || !strings.Contains(err.Error(), "packed Go ABI0 subword slots") {
		t.Fatalf("narrow Go call error: %v", err)
	}
}
