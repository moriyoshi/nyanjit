package machine_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/amd64"
	"github.com/moriyoshi/nyanjit/backend/target/arm64"
	"github.com/moriyoshi/nyanjit/backend/target/i386"
	"github.com/moriyoshi/nyanjit/backend/target/loong64"
	"github.com/moriyoshi/nyanjit/backend/target/ppc64"
	"github.com/moriyoshi/nyanjit/backend/target/riscv64"
)

func TestInvalidRotateHelper(t *testing.T) {
	arch := os.Getenv("NYANJIT_INVALID_ROTATE_HELPER")
	if arch == "" {
		t.Skip("subprocess only")
	}
	fmt.Println("READY")
	ins := machine.Instruction{Op: machine.OpShiftBy, Arithmetic: op.OpRotateLeft, Bits: 0, A: 1, B: 2, C: 3}
	var a machine.Assembler
	if arch == "arm64" {
		a = arm64.NewByteAssembler(abi.StackPlatform, 0)
	} else if arch == "loong64" {
		a = loong64.NewByteAssembler(abi.StackPlatform, 0)
	} else {
		a = riscv64.NewByteAssembler(abi.StackPlatform, 0)
	}
	if err := machine.Visit(ins, a); err == nil {
		t.Fatal("zero-width rotation accepted")
	}
	if arch == "arm64" {
		if arm64.PhysicalSize(ins, abi.StackNoUse, 0) != 0 {
			t.Fatal("invalid size")
		}
	} else if arch == "loong64" {
		if loong64.PhysicalSize(ins, abi.StackPlatform, 0) != 0 {
			t.Fatal("invalid size")
		}
	} else {
		if riscv64.PhysicalSize(ins, abi.StackPlatform, 0) != 0 {
			t.Fatal("invalid size")
		}
	}
}
func TestZeroWidthRotateBounded(t *testing.T) {
	for _, arch := range []string{"arm64", "riscv64", "loong64"} {
		t.Run(arch, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestInvalidRotateHelper$", "-test.v")
			cmd.Env = append(os.Environ(), "NYANJIT_INVALID_ROTATE_HELPER="+arch, "GOMEMLIMIT=64MiB", "GOMAXPROCS=2", "GORACE=atexit_sleep_ms=0")
			out, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			cmd.Stderr = cmd.Stdout
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			ready := make(chan struct{})
			logs := make(chan string, 1)
			go func() {
				var lines []string
				scanner := bufio.NewScanner(out)
				for scanner.Scan() {
					s := scanner.Text()
					lines = append(lines, s)
					if s == "READY" {
						close(ready)
					}
				}
				logs <- strings.Join(lines, "\n")
			}()
			select {
			case <-ready:
			case <-ctx.Done():
				cmd.Process.Kill()
				cmd.Wait()
				t.Fatal("helper startup timed out")
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("helper failed: %v\n%s", err, <-logs)
				}
			case <-time.After(time.Second):
				cmd.Process.Kill()
				<-done
				t.Fatal("zero-width rotate did not terminate within 1s after startup")
			}
		})
	}
}

func TestEncoderInvalidOperandTable(t *testing.T) {
	makers := map[string]func() machine.Assembler{
		"arm64":   func() machine.Assembler { return arm64.NewByteAssembler(abi.StackPlatform, 0) },
		"amd64":   func() machine.Assembler { return amd64.NewByteAssembler(abi.StackPlatform, 0) },
		"riscv64": func() machine.Assembler { return riscv64.NewByteAssembler(abi.StackPlatform, 0) },
		"loong64": func() machine.Assembler { return loong64.NewByteAssembler(abi.StackPlatform, 0) },
	}
	for arch, mk := range makers {
		t.Run(arch, func(t *testing.T) {
			cases := []machine.Instruction{{Op: machine.OpFloatBinary, Arithmetic: op.Opcode(200), Bits: 64, A: 1, B: 2, C: 3}}
			if arch == "arm64" {
				cases = append(cases, machine.Instruction{Op: machine.OpSelectFlags, Condition: 200}, machine.Instruction{Op: machine.OpFloatSpill, Bits: 64, A: backend.VectorRegister(1), Slot: 1 << 30})
			}
			if arch == "amd64" {
				for _, operation := range []machine.Opcode{machine.OpBinaryCarry, machine.OpFloatBinary, machine.OpShiftBy, machine.OpMulHigh, machine.OpCarryBit, machine.OpWordCarry, machine.OpCountZeros, machine.OpFloatBits} {
					cases = append(cases, machine.Instruction{Op: operation, Arithmetic: op.OpAdd, Bits: 64, A: 255, B: 255, C: 255, D: 255, E: 255})
				}
			}
			for _, ins := range cases {
				t.Run(fmt.Sprintf("%d/%d", ins.Op, ins.Condition), func(t *testing.T) {
					defer func() {
						if p := recover(); p != nil {
							t.Errorf("encoder panicked: %v", p)
						}
					}()
					a := mk()
					if err := machine.Visit(ins, a); err == nil {
						t.Errorf("invalid instruction accepted: %+v", ins)
					}
					if err := machine.Visit(machine.Instruction{Op: machine.OpReturn}, a); err == nil {
						t.Errorf("error was not sticky for %+v", ins)
					}
				})
			}
		})
	}
}

// OpMulWide names the 386 accumulator; other targets must not reinterpret it.
func TestMulWideIsI386Only(t *testing.T) {
	ins := machine.Instruction{Op: machine.OpMulWide, A: 1}
	for name, a := range map[string]machine.Assembler{
		"arm64":   arm64.NewByteAssembler(abi.StackPlatform, 0),
		"amd64":   amd64.NewByteAssembler(abi.StackPlatform, 0),
		"riscv64": riscv64.NewByteAssembler(abi.StackPlatform, 0),
		"loong64": loong64.NewByteAssembler(abi.StackPlatform, 0),
	} {
		if err := machine.Visit(ins, a); !errors.Is(err, machine.ErrUnsupportedOperation) {
			t.Errorf("%s: Visit(OpMulWide) = %v, want ErrUnsupportedOperation", name, err)
		}
	}
	for name, size := range map[string]int{
		"arm64":   arm64.PhysicalSize(ins, abi.StackPlatform, 0),
		"amd64":   amd64.PhysicalSize(ins, abi.StackPlatform, 0),
		"riscv64": riscv64.PhysicalSize(ins, abi.StackPlatform, 0),
		"loong64": loong64.PhysicalSize(ins, abi.StackPlatform, 0),
	} {
		if size != 0 {
			t.Errorf("%s: PhysicalSize(OpMulWide) = %d, want 0", name, size)
		}
	}
	a := i386.NewByteAssembler(abi.StackPlatform)
	if err := machine.Visit(ins, a); err != nil || string(a.Bytes()) != "\xf7\xe1" {
		t.Fatalf("i386: Visit(OpMulWide) = % x, %v; want f7 e1", a.Bytes(), err)
	}
}

// fuzzInstruction decodes every operand field of an instruction from data,
// so that flag, carry, branch-form, barrier and literal fields vary too.
func fuzzInstruction(data []byte) machine.Instruction {
	at := func(i int) byte { return data[i%len(data)] }
	word := func(i int) uint64 {
		var v uint64
		for k := range 8 {
			v = v<<8 | uint64(at(i+k))
		}
		return v
	}
	flags := at(4)
	ins := machine.Instruction{
		Op: machine.Opcode(at(0)), Arithmetic: op.Opcode(at(1)), Bits: at(2), Condition: op.BranchCondition(at(3)),
		Comparison: machine.ComparisonMode(at(5) & 3), ByteOrder: op.ByteOrder(at(5) >> 2 & 1), Barrier: machine.Barrier(at(5) >> 3 & 3),
		CarryMode: machine.CarryMode(at(5) >> 5 & 3), FlagRequest: machine.FlagMask(at(6) & 7),
		A: backend.Register(at(1)), B: backend.Register(at(2)), C: backend.Register(at(3)), D: backend.Register(at(7)), E: backend.Register(at(8)),
		HighA: backend.Register(at(9)), HighB: backend.Register(at(10)), HighC: backend.Register(at(11)),
		Imm: uint64(at(0)) * 16, Disp: uint64(at(1)) * 16, Slot: int(int8(at(2))),
		PreserveFlags: flags&1 != 0, Long: flags&2 != 0, Far: flags&4 != 0, Indexed: flags&8 != 0,
	}
	if flags&16 != 0 {
		ins.Imm = word(12) // full-width immediates, not just small multiples of 16
	}
	if flags&32 != 0 {
		ins.Disp = word(20)
	}
	if flags&64 != 0 {
		ins.CallAreaBytes = int(at(6)) * 8
	}
	if flags&128 != 0 && len(data) > 28 {
		ins.Data = data[28:min(len(data), 28+64)]
	}
	return ins
}

// Bounded mutation coverage exercises public dispatch without executing bytes.
// Frame/pool sizes stay small; zero rotations are separately process bounded.
// Every size query must agree with the encoding (rule L1: rejected is size 0).
func FuzzEncoderVisit(f *testing.F) {
	for _, seed := range [][]byte{{0, 0, 0, 0}, {1, 200, 255, 255}, {2, 12, 64, 1}, {3, 1, 32, 255},
		{byte(machine.OpBinary), byte(op.OpAdd), 64, 0, 1, 0, 7, 3, 4, 5, 6, 7},
		{byte(machine.OpJumpIf), 0, 0, 3, 6, 0, 0},
		{byte(machine.OpCarryBit), byte(op.OpAdd), 64, 0, 0, 0x20, 1},
		{byte(machine.OpBarrier), 0, 0, 0, 0, 0x08},
		{byte(machine.OpLiteralData), 0, 0, 0, 0x80, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 4 {
			return
		}
		ins := fuzzInstruction(data)
		// Branch and literal reach depend on layout; keep targets near.
		switch ins.Op {
		case machine.OpJump, machine.OpJumpIf, machine.OpBranchZero, machine.OpBranchBit, machine.OpLoop:
			ins.Imm = 64
		case machine.OpLiteralLoad:
			ins.Disp = 64
		}
		for _, stack := range []abi.StackPractice{abi.StackPlatform, abi.StackNoUse} {
			for arch := 0; arch < 5; arch++ {
				var a interface {
					machine.Assembler
					AssembledSize() int
					Err() error
				}
				var size func(machine.Instruction, abi.StackPractice, int) int
				switch arch {
				case 0:
					a, size = arm64.NewByteAssembler(stack, 0), arm64.PhysicalSize
					_, _ = arm64.ImplicitWrites(ins), arm64.FlagFacts(ins)
				case 1:
					a, size = amd64.NewByteAssembler(stack, 0), amd64.PhysicalSize
					_, _ = amd64.FlagFacts(ins), amd64.FlagEffect(ins)
				case 2:
					a, size = riscv64.NewByteAssembler(stack, 0), riscv64.PhysicalSize
					_ = riscv64.ImplicitWrites(ins, stack, 0)
				case 3:
					a, size = loong64.NewByteAssembler(stack, 0), loong64.PhysicalSize
					_ = loong64.ImplicitWrites(ins, stack, 0)
				case 4:
					a, size = i386.NewByteAssembler(stack), i386.PhysicalSize
					_ = i386.FlagEffect(ins)
				}
				err := machine.Visit(ins, a)
				if err == nil {
					err = a.Err()
				}
				got, want := size(ins, stack, 0), a.AssembledSize()
				if err != nil {
					want = 0
				}
				if got != want {
					t.Errorf("arch %d stack %d: size %d, encoding %d (%v) for %+v", arch, stack, got, want, err, ins)
				}
			}
		}
		profile, err := abi.NewPPC64Profile("ppc64le", abi.C)
		if err != nil {
			t.Fatal(err)
		}
		emitter, err := ppc64.NewEmitter(profile, abi.StackPlatform)
		if err != nil {
			t.Fatal(err)
		}
		_, _, _ = emitter.ImplicitWrites(ins), emitter.SpecialWrites(ins), emitter.FlagEffect(ins)
		want := 0
		if plan, err := emitter.Expand(ins, 0); err == nil && ins.Op != machine.OpLiteralData {
			a := ppc64.NewByteAssembler()
			for _, i := range plan {
				if err := a.Visit(i); err != nil {
					a = nil
					break
				}
			}
			if a != nil {
				want = a.AssembledSize()
			}
		}
		if got := emitter.Size(ins); ins.Op != machine.OpLiteralData && got != want {
			t.Errorf("ppc64: size %d, encoding %d for %+v", got, want, ins)
		}
	})
}
