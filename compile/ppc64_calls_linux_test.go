//go:build linux && (ppc64 || ppc64le)

package compile_test

import (
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/execution"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/target/ppc64"
)

func ppcNativeCode(t *testing.T, instructions ...ppc64.Instruction) []byte {
	t.Helper()
	order := ir.ByteOrderBigEndian
	if runtime.GOARCH == "ppc64le" {
		order = ir.ByteOrderLittleEndian
	}
	assembler, err := ppc64.NewByteAssembler(order)
	if err != nil {
		t.Fatal(err)
	}
	for i, ins := range instructions {
		ins.PC = 4 * i
		if err := assembler.Visit(ins); err != nil {
			t.Fatal(err)
		}
	}
	return assembler.Bytes()
}

// This C leaf writes every parameter home in its caller's linkage area. The
// JIT's live-value spills must be above those homes. It also overwrites R0 and
// R2, and (for ELFv1) consumes the descriptor's TOC and environment.
func ppcCProbe(t *testing.T, convention abi.PPC64CABI) (uintptr, func()) {
	t.Helper()
	profile, err := abi.NewPPC64Profile(runtime.GOARCH, abi.C, convention)
	if err != nil {
		t.Fatal(err)
	}
	sequence := []ppc64.Instruction{}
	for r := machine.Register(3); r <= 10; r++ {
		sequence = append(sequence, ppc64.Instruction{Op: ppc64.Std, A: r, B: 1, Imm: int64(profile.Frame().ParameterOffset) + int64(r-3)*8})
	}
	if convention == abi.PPC64ELFv1 {
		sequence = append(sequence, ppc64.Instruction{Op: ppc64.Add, A: 3, B: 3, C: 2}, ppc64.Instruction{Op: ppc64.Add, A: 3, B: 3, C: 11})
	}
	for r := machine.Register(4); r <= 10; r++ {
		sequence = append(sequence, ppc64.Instruction{Op: ppc64.Add, A: 3, B: 3, C: r})
	}
	sequence = append(sequence, ppc64.Instruction{Op: ppc64.Addi, A: 0, Imm: 999}, ppc64.Instruction{Op: ppc64.Addi, A: 2, Imm: 777}, ppc64.Instruction{Op: ppc64.Blr})
	memory, err := execution.Map(ppcNativeCode(t, sequence...))
	if err != nil {
		t.Fatal(err)
	}
	address := execution.Entry(memory)
	var descriptor []byte
	if convention == abi.PPC64ELFv1 {
		descriptor, err = execution.Scratch(24)
		if err != nil {
			t.Fatal(err)
		}
		binary.NativeEndian.PutUint64(descriptor, uint64(address))
		binary.NativeEndian.PutUint64(descriptor[8:], 100)
		binary.NativeEndian.PutUint64(descriptor[16:], 1000)
		address = uintptr(unsafe.Pointer(&descriptor[0]))
	}
	return address, func() {
		if descriptor != nil {
			_ = execution.FreeScratch(descriptor)
		}
		_ = execution.Unmap(memory)
	}
}

func TestPPC64CCallConventionsAndLiveSpills(t *testing.T) {
	conventions := []abi.PPC64CABI{abi.PPC64ELFv2}
	if runtime.GOARCH == "ppc64" {
		conventions = append(conventions, abi.PPC64ELFv1)
	}
	for _, calleeABI := range conventions {
		address, closeProbe := ppcCProbe(t, calleeABI)
		defer closeProbe()
		effect, err := abi.PreflightCall(abi.CallSpec{ABI: abi.C, ArgumentCount: 8, PPC64CABI: calleeABI})
		if err != nil {
			t.Fatal(err)
		}
		for _, entryABI := range conventions {
			for _, managed := range []bool{false, true} {
				entryFlavors := []abi.Flavor{abi.C}
				if managed {
					entryFlavors = append(entryFlavors, abi.Go)
				}
				for _, flavor := range entryFlavors {
					name := fmt.Sprintf("callee-ELFv%d/entry-ELFv%d", calleeABI, entryABI)
					t.Run(name+"/managed="+map[bool]string{true: "yes", false: "no"}[managed]+"/entry="+map[abi.Flavor]string{abi.C: "C", abi.Go: "Go"}[flavor], func(t *testing.T) {
						b := nyanjit.NewBuilder()
						live := b.Param()
						args := []nyanjit.Value{b.Const(1), b.Const(2), b.Const(3), b.Const(4), b.Const(5), b.Const(6), b.Const(7), b.Const(8)}
						result := b.CallCWithEffect(b.Const(uint64(address)), effect, args...)
						b.Return(b.Add(live, result))
						options := abi.CompileOptions{ABI: flavor, PPC64CABI: entryABI, Stack: abi.StackPlatform, StackBytesAvailable: 640, AssumePointerFree: managed}
						if flavor == abi.Go {
							options.PPC64CABI = abi.PPC64CABIUnspecified
						}
						program := finishPPC(t, b)
						var template *compile.CodeTemplate
						if managed {
							template, err = compile.PrepareGo(program, options)
						} else {
							template, err = compile.PrepareForeign(program, options)
						}
						if err != nil {
							t.Fatal(err)
						}
						code, err := template.Bind(nil)
						if err != nil {
							t.Fatal(err)
						}
						defer code.Close()
						got, err := code.Call(42)
						want := uint64(42 + 36)
						if calleeABI == abi.PPC64ELFv1 {
							want += 1100
						}
						if err != nil || uint64(got) != want {
							t.Fatalf("call = %d, %v; want %d", got, err, want)
						}
						listingOptions := options
						listingOptions.ManagedCalls = managed
						assembly, err := compile.EmitAssembly(program, listingOptions)
						if err != nil {
							t.Fatal(err)
						}
						if !strings.Contains(assembly, "bctrl") {
							t.Fatalf("no indirect call:\n%s", assembly)
						}
					})
				}
			}
		}
	}
}

func TestPPC64SyscallResultsAndLiveValues(t *testing.T) {
	for _, test := range []struct {
		nr   uint64
		args []uint64
		want int64
	}{
		{syscall.SYS_GETPID, nil, int64(syscall.Getpid())},
		{syscall.SYS_CLOSE, []uint64{^uint64(0)}, -int64(syscall.EBADF)},
		{0x7fffffff, nil, -int64(syscall.ENOSYS)},
	} {
		b := nyanjit.NewBuilder()
		live := b.Param()
		arguments := []nyanjit.Value{}
		for _, arg := range test.args {
			arguments = append(arguments, b.Const(arg))
		}
		result := b.Syscall(b.Const(test.nr), arguments...)
		b.Return(b.Add(result, live))
		bytes, report, err := compile.Emit(finishPPC(t, b), abi.CompileOptions{Stack: abi.StackPlatform, StackBytesAvailable: 512})
		if err != nil {
			t.Fatal(err)
		}
		code, err := execution.NewCode(bytes, execution.CodeOptions{Arity: 1}, nil)
		if err != nil {
			t.Fatal(err)
		}
		got, err := code.Call(17)
		_ = code.Close()
		if err != nil || int64(got) != test.want+17 {
			t.Fatalf("syscall %d = %d, %v; want %d", test.nr, int64(got), err, test.want+17)
		}
		if !report.HasSystemCalls || report.HasFunctionCalls {
			t.Fatalf("syscall report = %+v", report)
		}
	}
}

//go:noinline
func ppcTwelveGoArgs(a, b, c, d, e, f, g, h, i, j, k, l uint64) uint64 {
	var frame [8192]byte
	frame[0] = byte(a)
	runtime.GC()
	runtime.KeepAlive(frame)
	return a + 2*b + 3*c + 4*d + 5*e + 6*f + 7*g + 8*h + 9*i + 10*j + 11*k + 12*l
}

func TestPPC64GoCallArityAndCEntryPreservation(t *testing.T) {
	for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
		b := nyanjit.NewBuilder()
		first, second := b.Param(), b.Param()
		args := []nyanjit.Value{second, first}
		for i := 3; i <= 12; i++ {
			args = append(args, b.Const(uint64(i)))
		}
		called := b.CallGo(b.Const(uint64(reflect.ValueOf(ppcTwelveGoArgs).Pointer())), args...)
		b.Return(b.Add(called, first))
		template, err := compile.PrepareGo(finishPPC(t, b), abi.CompileOptions{ABI: flavor, Stack: abi.StackPlatform, StackBytesAvailable: 640, AssumePointerFree: true})
		if err != nil {
			t.Fatal(err)
		}
		code, err := template.Bind(nil)
		if err != nil {
			t.Fatal(err)
		}
		defer code.Close()
		got, err := code.Call(1, 2)
		want := uint64(2 + 2)
		for i := 3; i <= 12; i++ {
			want += uint64(i * i)
		}
		want++
		if err != nil || uint64(got) != want {
			t.Fatalf("entry %d = %d, %v; want %d", flavor, got, err, want)
		}
		if flavor == abi.C {
			report := template.Report()
			for _, name := range []string{"R2", "R14", "R29", "R31"} {
				if slices.Contains(report.ClobberedRegisters, name) {
					t.Fatalf("restored %s reported destroyed", name)
				}
			}
			for _, name := range []string{"LR", "CR2", "CR3", "CR4"} {
				if slices.Contains(report.ClobberedSpecialRegisters, name) {
					t.Fatalf("restored %s reported destroyed", name)
				}
			}
			if !slices.Contains(report.ClobberedSpecialRegisters, "CTR") || !slices.Contains(report.ClobberedSpecialRegisters, "CR5") {
				t.Fatalf("missing volatile resources: %v", report.ClobberedSpecialRegisters)
			}
			for r := 14; r < 32; r++ {
				if slices.Contains(report.ClobberedFPRegisters, "F"+strconv.Itoa(r)) {
					t.Fatalf("restored F%d reported destroyed", r)
				}
			}
			for r := 20; r < 32; r++ {
				if slices.Contains(report.ClobberedFPRegisters, "V"+strconv.Itoa(r)) {
					t.Fatalf("restored V%d reported destroyed", r)
				}
			}
			if !slices.Contains(report.ClobberedFPRegisters, "V14") {
				t.Fatal("volatile V14 was incorrectly reported as preserved")
			}
		}
	}
}

func TestPPC64RejectsConflictingCallConvention(t *testing.T) {
	if _, err := abi.PreflightCall(abi.CallSpec{ABI: abi.Go, PPC64CABI: abi.PPC64ELFv2}); !errors.Is(err, abi.ErrInvalidSpec) {
		t.Fatalf("Go call with C variant = %v", err)
	}
	if runtime.GOARCH == "ppc64le" {
		if _, err := abi.PreflightCall(abi.CallSpec{ABI: abi.C, PPC64CABI: abi.PPC64ELFv1}); !errors.Is(err, abi.ErrUnsupportedABI) {
			t.Fatalf("LE ELFv1 = %v", err)
		}
	}
}

func TestPPC64PreservationTransfersAllFPAndVectorBits(t *testing.T) {
	memory, err := execution.Scratch(96)
	if err != nil {
		t.Fatal(err)
	}
	defer execution.FreeScratch(memory)
	for i := 0; i < 64; i++ {
		memory[i] = byte(i + 1)
	}
	profile, _ := abi.NewPPC64Profile(runtime.GOARCH, abi.C, abi.PPC64ELFv2)
	emitter, _ := ppc64.NewEmitter(profile, abi.StackPlatform)
	order := ir.ByteOrderBigEndian
	if runtime.GOARCH == "ppc64le" {
		order = ir.ByteOrderLittleEndian
	}
	assembler, _ := ppc64.NewByteAssembler(order)
	pc := 0
	concrete := func(ins ppc64.Instruction) {
		ins.PC = pc
		if err := assembler.Visit(ins); err != nil {
			t.Fatal(err)
		}
		pc += 4
	}
	allocated := func(ins machine.Instruction) {
		seq, err := emitter.Expand(ins, pc)
		if err != nil {
			t.Fatal(err)
		}
		for _, native := range seq {
			concrete(native)
		}
	}
	allocated(machine.Instruction{Op: machine.OpFrameEnter, Imm: 256})
	concrete(ppc64.Instruction{Op: ppc64.Lfd, A: 14, B: 3})
	concrete(ppc64.Instruction{Op: ppc64.Addi, A: 11, Imm: 16})
	concrete(ppc64.Instruction{Op: ppc64.Lvx, A: 20, B: 3, C: 11})
	allocated(machine.Instruction{Op: machine.OpConst, A: 11, Imm: 0x5a5a5a5a})
	concrete(ppc64.Instruction{Op: ppc64.Mtcrf, A: 11, Imm: 255})
	allocated(machine.Instruction{Op: machine.OpSaveCFloat, A: 14, Slot: 0})
	allocated(machine.Instruction{Op: machine.OpSaveCVector, A: 20, Slot: 2})
	allocated(machine.Instruction{Op: machine.OpSaveCondition, Slot: 4})
	concrete(ppc64.Instruction{Op: ppc64.Lfd, A: 14, B: 3, Imm: 32})
	concrete(ppc64.Instruction{Op: ppc64.Addi, A: 11, Imm: 48})
	concrete(ppc64.Instruction{Op: ppc64.Lvx, A: 20, B: 3, C: 11})
	allocated(machine.Instruction{Op: machine.OpConst, A: 11, Imm: 0xa5a5a5a5})
	concrete(ppc64.Instruction{Op: ppc64.Mtcrf, A: 11, Imm: 255})
	allocated(machine.Instruction{Op: machine.OpRestoreCFloat, A: 14, Slot: 0})
	allocated(machine.Instruction{Op: machine.OpRestoreCVector, A: 20, Slot: 2})
	allocated(machine.Instruction{Op: machine.OpRestoreCondition, Slot: 4})
	concrete(ppc64.Instruction{Op: ppc64.Stfd, A: 14, B: 3, Imm: 64})
	concrete(ppc64.Instruction{Op: ppc64.Addi, A: 11, Imm: 80})
	concrete(ppc64.Instruction{Op: ppc64.Stvx, A: 20, B: 3, C: 11})
	concrete(ppc64.Instruction{Op: ppc64.Mfcr, A: 4})
	concrete(ppc64.Instruction{Op: ppc64.Std, A: 4, B: 3, Imm: 72})
	allocated(machine.Instruction{Op: machine.OpConst, A: 3, Imm: 42})
	allocated(machine.Instruction{Op: machine.OpFrameLeave, Imm: 256})
	allocated(machine.Instruction{Op: machine.OpReturn})
	code, err := execution.NewCode(assembler.Bytes(), execution.CodeOptions{ABI: abi.Go, Arity: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	got, err := code.Call(uintptr(unsafe.Pointer(&memory[0])))
	if err != nil || got != 42 {
		t.Fatalf("preservation probe = %d, %v", got, err)
	}
	if !slices.Equal(memory[:8], memory[64:72]) {
		t.Fatalf("F14 changed: %x != %x", memory[:8], memory[64:72])
	}
	if !slices.Equal(memory[16:32], memory[80:96]) {
		t.Fatalf("V20 changed: %x != %x", memory[16:32], memory[80:96])
	}
	if cr := binary.NativeEndian.Uint64(memory[72:80]); cr != 0xa55a55a5 {
		t.Fatalf("CR preservation = %#x; want %#x", cr, uint64(0xa55a55a5))
	}
}

func TestPPC64ManagedCLeafNeedsNoPreservationFrame(t *testing.T) {
	b := nyanjit.NewBuilder()
	b.Return(b.Const(42))
	template, err := compile.PrepareGo(finishPPC(t, b), abi.CompileOptions{ABI: abi.C, Stack: abi.StackPlatform, AssumePointerFree: true})
	if err != nil {
		t.Fatal(err)
	}
	if frame := template.Report().StackBytes; frame != 0 {
		t.Fatalf("C leaf frame = %d", frame)
	}
	code, err := template.Bind(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	got, err := code.Call()
	if err != nil || got != 42 {
		t.Fatalf("C leaf = %d, %v", got, err)
	}
}

func TestPPC64BlockingCCallAllowsGC(t *testing.T) {
	memory, err := execution.Scratch(16)
	if err != nil {
		t.Fatal(err)
	}
	defer execution.FreeScratch(memory)
	binary.NativeEndian.PutUint64(memory[8:], 100_000_000)
	native, err := execution.Map(ppcNativeCode(t,
		ppc64.Instruction{Op: ppc64.Addi, A: 4},
		ppc64.Instruction{Op: ppc64.Addi, A: 0, Imm: syscall.SYS_NANOSLEEP},
		ppc64.Instruction{Op: ppc64.Sc},
		ppc64.Instruction{Op: ppc64.Xor, A: 0, B: 0, C: 0},
		ppc64.Instruction{Op: ppc64.Addi, A: 3},
		ppc64.Instruction{Op: ppc64.Blr},
	))
	if err != nil {
		t.Fatal(err)
	}
	defer execution.Unmap(native)
	address := execution.Entry(native)
	convention, _ := abi.DefaultPPC64CABI(runtime.GOARCH)
	if convention == abi.PPC64ELFv1 {
		descriptor, err := execution.Scratch(24)
		if err != nil {
			t.Fatal(err)
		}
		defer execution.FreeScratch(descriptor)
		binary.NativeEndian.PutUint64(descriptor, uint64(address))
		address = uintptr(unsafe.Pointer(&descriptor[0]))
	}
	b := nyanjit.NewBuilder()
	live := b.Param()
	called := b.CallC(b.Const(uint64(address)), b.Const(uint64(uintptr(unsafe.Pointer(&memory[0])))))
	b.Return(b.Add(live, called))
	code, err := compile.CompileGo(finishPPC(t, b), abi.CompileOptions{ABI: abi.Go, Stack: abi.StackPlatform, StackBytesAvailable: 640, AssumePointerFree: true})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	original := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(original)
	result := make(chan uint64, 1)
	go func() {
		got, callErr := code.Call(17)
		if callErr != nil {
			result <- ^uint64(0)
		} else {
			result <- uint64(got)
		}
	}()
	time.Sleep(10 * time.Millisecond)
	runtime.GC()
	select {
	case got := <-result:
		if got != 17 {
			t.Fatalf("blocking C call = %d", got)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("blocking C gateway did not release the processor")
	}
}
