// Command stackvm compiles a small stack bytecode to native code with only
// the NyanJIT backend module: target policies and assemblers, the machine
// instruction stream, register allocation and execution. It does not use
// NyanJIT's IR, Builder or compile packages. It describes each program as a
// regalloc.Function and emits it through the regalloc.Allocator protocol, so
// one emitter works with any allocator: by default regalloc.Online, which
// allocates as it walks and carries locals from block to block in the
// registers their edges leave them in, or regalloc.Batch, which allocates the
// whole function at once.
//
//	go run ./examples/stackvm             # compile and run on this host
//	go run ./examples/stackvm -S -arch riscv64   # print a listing for any target
//	go run ./examples/stackvm -alloc batch       # allocate whole functions at once
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/regalloc"
)

// Programs are the examples main runs; the tests run them too.
var Programs = []*Program{
	{
		// sum of i*i for i = n down to 1
		Name: "squares", Params: 1, Locals: 2,
		Code: []Insn{
			{Get, 0}, {Jz, 13}, // 0: while n != 0 (locals start at zero)
			{Get, 1}, {Get, 0}, {Dup, 0}, {Mul, 0}, {Add, 0}, {Set, 1}, // acc += n*n
			{Get, 0}, {Push, 1}, {Sub, 0}, {Set, 0}, // n--
			{Jmp, 0},
			{Get, 1}, {Ret, 0}, // 13
		},
	},
	{
		// (x+1) + (x+2) + ... + (x+12), with every term pushed before the
		// first addition, so the stack outgrows the registers and spills.
		Name: "deep", Params: 1, Locals: 1, Code: deep(12),
	},
	{
		// sum of i*j for i = n down to 1 and j = 3 down to 1
		Name: "nested", Params: 1, Locals: 3,
		Code: []Insn{
			{Get, 0}, {Jz, 22}, // 0: while n != 0
			{Push, 3}, {Set, 2}, // j = 3
			{Get, 2}, {Jz, 17}, // 4: while j != 0
			{Get, 1}, {Get, 0}, {Get, 2}, {Mul, 0}, {Add, 0}, {Set, 1}, // acc += n*j
			{Get, 2}, {Push, 1}, {Sub, 0}, {Set, 2}, {Jmp, 4}, // j--
			{Get, 0}, {Push, 1}, {Sub, 0}, {Set, 0}, {Jmp, 0}, // 17: n--
			{Get, 1}, {Ret, 0}, // 22
		},
	},
	{
		// six locals updated on every iteration of an n-step loop
		Name: "locals", Params: 1, Locals: 7,
		Code: []Insn{
			{Get, 0}, {Jz, 35}, // 0: while n != 0
			{Get, 1}, {Get, 2}, {Add, 0}, {Set, 1},
			{Get, 2}, {Get, 3}, {Xor, 0}, {Push, 1}, {Add, 0}, {Set, 2},
			{Get, 3}, {Get, 0}, {Add, 0}, {Set, 3},
			{Get, 4}, {Push, 3}, {Mul, 0}, {Get, 1}, {Add, 0}, {Set, 4},
			{Get, 5}, {Get, 4}, {Sub, 0}, {Set, 5},
			{Get, 6}, {Get, 5}, {Or, 0}, {Set, 6},
			{Get, 0}, {Push, 1}, {Sub, 0}, {Set, 0}, {Jmp, 0},
			{Get, 1}, {Get, 2}, {Add, 0}, {Get, 3}, {Add, 0}, {Get, 4}, {Add, 0}, // 35
			{Get, 5}, {Add, 0}, {Get, 6}, {Add, 0}, {Ret, 0},
		},
	},
	{
		// a three-argument mix of logical and arithmetic operations
		Name: "mix", Params: 3, Locals: 3,
		Code: []Insn{
			{Get, 0}, {Get, 1}, {Xor, 0}, {Get, 2}, {And, 0},
			{Get, 0}, {Get, 2}, {Or, 0}, {Sub, 0},
			{Push, 0x55}, {Mul, 0}, {Ret, 0},
		},
	},
}

func deep(terms int) []Insn {
	var code []Insn
	for k := 1; k <= terms; k++ {
		code = append(code, Insn{Get, 0}, Insn{Push, uintptr(k)}, Insn{Add, 0})
	}
	for k := 1; k < terms; k++ {
		code = append(code, Insn{Add, 0})
	}
	return append(code, Insn{Ret, 0})
}

// Run compiles p for the host with regalloc.Online and calls it through
// execution.Code.
func Run(p *Program, args ...uintptr) (uintptr, error) {
	return RunWith(&Compiler{Alloc: &regalloc.Online{}}, p, args...)
}

// RunWith is Run with a choice of compiler.
func RunWith(c *Compiler, p *Program, args ...uintptr) (uintptr, error) {
	t, err := newTarget(runtime.GOARCH)
	if err != nil {
		return 0, err
	}
	f, err := c.Compile(p, t, false)
	if err != nil {
		return 0, err
	}
	code, err := execution.NewCode(f.Code, execution.CodeOptions{
		ABI: abi.C, Foreign: true, Arity: p.Params, StackBytes: f.FrameBytes,
	}, nil)
	if err != nil {
		return 0, err
	}
	defer code.Close()
	return code.Call(args...)
}

func main() {
	arch := flag.String("arch", runtime.GOARCH, "target architecture: amd64, arm64, 386, riscv64, loong64 or ppc64le")
	listing := flag.Bool("S", false, "print assembly listings instead of running")
	alloc := flag.String("alloc", "online", "register allocator: online (regalloc.Online) or batch (regalloc.Batch)")
	flag.Parse()
	var a regalloc.Allocator
	switch *alloc {
	case "online":
		a = &regalloc.Online{}
	case "batch":
		a = &regalloc.Batch{}
	default:
		fmt.Fprintf(os.Stderr, "stackvm: unknown allocator %q\n", *alloc)
		os.Exit(2)
	}
	if err := run(&Compiler{Alloc: a}, *arch, *listing); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(c *Compiler, arch string, listing bool) error {
	if listing {
		t, err := newTarget(arch)
		if err != nil {
			return err
		}
		for _, p := range Programs {
			f, err := c.Compile(p, t, true)
			if err != nil {
				return err
			}
			fmt.Printf("%s (%s, frame %d bytes):\n%s\n\n", p.Name, arch, f.FrameBytes, f.Listing)
		}
		return nil
	}
	if arch != runtime.GOARCH {
		return fmt.Errorf("stackvm: cannot run %s code on %s; use -S for a listing", arch, runtime.GOARCH)
	}
	for _, p := range Programs {
		args := []uintptr{10, 3, 7}[:p.Params]
		got, err := RunWith(c, p, args...)
		if err != nil {
			return err
		}
		want, err := Interpret(p, args...)
		if err != nil {
			return err
		}
		fmt.Printf("%s%v = %d (interpreter: %d)\n", p.Name, args, got, want)
		if got != want {
			return fmt.Errorf("stackvm: %s: compiled code returned %d, interpreter %d", p.Name, got, want)
		}
	}
	return nil
}
