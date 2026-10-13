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

// Bounded mutation coverage exercises public dispatch without executing bytes.
// Frame/pool sizes stay small; zero rotations are separately process bounded.
func FuzzEncoderVisit(f *testing.F) {
	for _, seed := range [][]byte{{0, 0, 0, 0}, {1, 200, 255, 255}, {2, 12, 64, 1}, {3, 1, 32, 255}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 4 {
			return
		}
		for arch := 0; arch < 5; arch++ {
			var a machine.Assembler
			switch arch {
			case 0:
				a = arm64.NewByteAssembler(abi.StackPlatform, 0)
			case 1:
				a = amd64.NewByteAssembler(abi.StackPlatform, 0)
			case 2:
				a = riscv64.NewByteAssembler(abi.StackPlatform, 0)
			case 3:
				a = loong64.NewByteAssembler(abi.StackPlatform, 0)
			case 4:
				a = i386.NewByteAssembler(abi.StackPlatform)
			}
			ins := machine.Instruction{Op: machine.Opcode(data[0]), Arithmetic: op.Opcode(data[1]), Bits: data[2], Condition: op.BranchCondition(data[3]), A: backend.Register(data[1]), B: backend.Register(data[2]), C: backend.Register(data[3]), D: backend.Register(data[1]), E: backend.Register(data[3]), Imm: uint64(data[0]) * 16, Disp: uint64(data[1]) * 16, Slot: int(int8(data[2]))}
			_ = machine.Visit(ins, a)
			if arch == 4 {
				_ = i386.PhysicalSize(ins, abi.StackPlatform, 0)
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
		ins := machine.Instruction{Op: machine.Opcode(data[0]), Arithmetic: op.Opcode(data[1]), Bits: data[2], Condition: op.BranchCondition(data[3]), A: backend.Register(data[1]), B: backend.Register(data[2]), C: backend.Register(data[3]), Imm: uint64(data[0]) * 16, Slot: int(int8(data[2]))}
		if plan, err := emitter.Expand(ins, 0); err == nil {
			a := ppc64.NewByteAssembler()
			for _, i := range plan {
				if err := a.Visit(i); err != nil {
					break
				}
			}
		}
	})
}
