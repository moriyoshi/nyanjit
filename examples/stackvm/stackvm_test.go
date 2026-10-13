package main

import (
	"errors"
	"github.com/moriyoshi/nyanjit/backend/regalloc"
	"math/rand"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
)

// compilers are the allocators, compared throughout.
var compilers = []struct {
	name  string
	alloc func() regalloc.Allocator
}{
	{"online", func() regalloc.Allocator { return &regalloc.Online{} }},
	{"batch", func() regalloc.Allocator { return &regalloc.Batch{} }},
}

// reusedCompilers returns a Compiler per allocator, for tests that compile
// many programs with each.
func reusedCompilers() []*Compiler {
	var cs []*Compiler
	for _, c := range compilers {
		cs = append(cs, &Compiler{Alloc: c.alloc()})
	}
	return cs
}

func hostSupported(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" || !slices.Contains(architectures, runtime.GOARCH) {
		t.Skipf("no native execution on %s/%s", runtime.GOOS, runtime.GOARCH)
	}
}

func TestProgramsMatchInterpreter(t *testing.T) {
	hostSupported(t)
	inputs := []uintptr{0, 1, 2, 10, 1000, ^uintptr(0), ^uintptr(0) >> 1}
	for _, c := range compilers {
		cc := &Compiler{Alloc: c.alloc()} // reused across programs
		for _, p := range Programs {
			for _, x := range inputs {
				if (p.Name == "squares" || p.Name == "nested" || p.Name == "locals") && x > 1000 {
					continue // the loops run x times
				}
				args := []uintptr{x, x*7 + 3, x ^ 0x5a}[:p.Params]
				got, err := RunWith(cc, p, args...)
				if err != nil {
					t.Fatal(err)
				}
				want, err := Interpret(p, args...)
				if err != nil {
					t.Fatal(err)
				}
				if got != want {
					t.Errorf("%s %s%v = %d, interpreter %d", c.name, p.Name, args, got, want)
				}
			}
		}
	}
}

// Random straight-line programs exercise spills and reloads: their stacks
// grow past every target's register pool.
func randomProgram(r *rand.Rand, params int) *Program {
	p := &Program{Name: "random", Params: params, Locals: params + 2}
	depth := 0
	for len(p.Code) < 60 || depth > 1 {
		switch choice := r.Intn(10); {
		case depth < 2 || choice < 3 && len(p.Code) < 60:
			if r.Intn(2) == 0 {
				p.Code = append(p.Code, Insn{Push, uintptr(r.Intn(1000))})
			} else {
				p.Code = append(p.Code, Insn{Get, uintptr(r.Intn(p.Locals))})
			}
			depth++
		case choice == 3 && len(p.Code) < 60:
			p.Code = append(p.Code, Insn{Dup, 0})
			depth++
		case choice == 4:
			p.Code = append(p.Code, Insn{Set, uintptr(r.Intn(p.Locals))})
			depth--
		default:
			p.Code = append(p.Code, Insn{[]Op{Add, Sub, Mul, And, Or, Xor}[r.Intn(6)], 0})
			depth--
		}
	}
	if depth == 0 {
		p.Code = append(p.Code, Insn{Get, 0})
	}
	return &Program{Name: p.Name, Params: p.Params, Locals: p.Locals, Code: append(p.Code, Insn{Ret, 0})}
}

func TestRandomProgramsMatchInterpreter(t *testing.T) {
	hostSupported(t)
	r := rand.New(rand.NewSource(1))
	reused := reusedCompilers()
	spilled := false
	for i := 0; i < 200; i++ {
		p := randomProgram(r, 1+i%3)
		deepest, err := p.check()
		if err != nil {
			t.Fatal(err)
		}
		spilled = spilled || deepest > 8
		args := []uintptr{uintptr(r.Uint64()), uintptr(r.Intn(100)), 7}[:p.Params]
		want, _ := Interpret(p, args...)
		for k, c := range compilers {
			got, err := RunWith(reused[k], p, args...)
			if err != nil {
				t.Fatalf("%s program %d: %v", c.name, i, err)
			}
			if got != want {
				t.Fatalf("%s program %d %v: compiled %d, interpreter %d\n%v", c.name, i, args, got, want, p.Code)
			}
		}
	}
	if !spilled {
		t.Fatal("no random program outgrew the registers")
	}
}

// Every target assembles every program, on any host.
func TestCrossAssembly(t *testing.T) {
	for _, arch := range architectures {
		target, err := newTarget(arch)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range compilers {
			cc := &Compiler{Alloc: c.alloc()} // reused across programs and modes
			for _, p := range append(Programs, structuredPrograms(arch)...) {
				f, err := cc.Compile(p, target, false)
				if err != nil {
					t.Fatalf("%s %s %s: %v", c.name, arch, p.Name, err)
				}
				listing, err := cc.Compile(p, target, true)
				if err != nil {
					t.Fatalf("%s %s %s: %v", c.name, arch, p.Name, err)
				}
				if len(f.Code) == 0 || strings.Count(listing.Listing, "\n") < len(p.Code)/4 {
					t.Errorf("%s %s %s: %d bytes, listing:\n%s", c.name, arch, p.Name, len(f.Code), listing.Listing)
				}
			}
		}
	}
}

func TestRejectsUnbalancedStack(t *testing.T) {
	target, err := newTarget("amd64")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []*Program{
		{Name: "underflow", Code: []Insn{{Add, 0}, {Ret, 0}}},
		{Name: "jump with values", Params: 1, Locals: 1, Code: []Insn{{Get, 0}, {Get, 0}, {Jz, 0}, {Ret, 0}}},
		{Name: "no return", Code: []Insn{{Push, 1}, {Drop, 0}}},
	} {
		for _, c := range compilers {
			if _, err := (&Compiler{Alloc: c.alloc()}).Compile(p, target, false); err == nil {
				t.Errorf("%s: %s compiled", c.name, p.Name)
			}
		}
	}
}

// Locals live in registers and, past them, in frame slots: a program that
// keeps more locals live across a loop than the registers and slotLimit
// hold together does not fit.
func TestRejectsLocalsBeyondSlotLimit(t *testing.T) {
	target, err := newTarget("amd64")
	if err != nil {
		t.Fatal(err)
	}
	// Count local 0 down to zero, then sum all the others.
	live := func(locals int) *Program {
		const exit = 7
		code := []Insn{{Get, 0}, {Jz, exit}, {Get, 0}, {Push, 1}, {Sub, 0}, {Set, 0}, {Jmp, 0}, {Get, 1}}
		for i := 2; i < locals; i++ {
			code = append(code, Insn{Get, uintptr(i)}, Insn{Add, 0})
		}
		return &Program{Name: "locals", Params: 1, Locals: locals, Code: append(code, Insn{Ret, 0})}
	}
	for _, c := range compilers {
		if _, err := (&Compiler{Alloc: c.alloc()}).Compile(live(2*slotLimit), target, false); !errors.Is(err, backend.ErrBackendLimit) {
			t.Fatalf("%s: Compile = %v, want ErrBackendLimit", c.name, err)
		}
		if _, err := (&Compiler{Alloc: c.alloc()}).Compile(live(slotLimit/4), target, false); err != nil {
			t.Fatalf("%s: Compile within the limit: %v", c.name, err)
		}
	}
}

// structuredProgram builds random if/else statements and bounded loops over
// locals, so values cross blocks in registers and slots.
type structuredProgram struct {
	r      *rand.Rand
	locals int
	code   []Insn
}

func (b *structuredProgram) emit(op Op, arg uintptr) int {
	b.code = append(b.code, Insn{op, arg})
	return len(b.code) - 1
}

// expression pushes one value computed from locals and constants.
func (b *structuredProgram) expression(depth int) {
	if depth == 0 || b.r.Intn(3) == 0 {
		if b.r.Intn(3) == 0 {
			b.emit(Push, uintptr(b.r.Intn(50)))
		} else {
			b.emit(Get, uintptr(b.r.Intn(b.locals)))
		}
		return
	}
	b.expression(depth - 1)
	b.expression(depth - 1)
	b.emit([]Op{Add, Sub, Mul, And, Or, Xor}[b.r.Intn(6)], 0)
}

// statements emits n statements; loop locals are those at least counter.
func (b *structuredProgram) statements(n, nesting, counter int) {
	for range n {
		switch choice := b.r.Intn(6); {
		case choice == 0 && nesting < 2:
			// if (e) { ... } else { ... }
			b.expression(2)
			jz := b.emit(Jz, 0)
			b.statements(1+b.r.Intn(3), nesting+1, counter)
			jmp := b.emit(Jmp, 0)
			b.code[jz].Arg = uintptr(len(b.code))
			b.statements(1+b.r.Intn(3), nesting+1, counter)
			b.code[jmp].Arg = uintptr(len(b.code))
		case choice == 1 && nesting < 2 && counter < b.locals:
			// c = k; while (c) { ...; c = c - 1 }, with c reserved.
			c := counter
			b.emit(Push, uintptr(1+b.r.Intn(4)))
			b.emit(Set, uintptr(c))
			top := b.emit(Get, uintptr(c))
			jz := b.emit(Jz, 0)
			b.statements(1+b.r.Intn(3), nesting+1, counter+1)
			b.emit(Get, uintptr(c))
			b.emit(Push, 1)
			b.emit(Sub, 0)
			b.emit(Set, uintptr(c))
			b.emit(Jmp, uintptr(top))
			b.code[jz].Arg = uintptr(len(b.code))
		default:
			// local = e; the top three locals are loop counters
			b.expression(2)
			b.emit(Set, uintptr(b.r.Intn(b.locals-3)))
		}
	}
}

func randomStructured(r *rand.Rand, params, locals int) *Program {
	b := &structuredProgram{r: r, locals: locals}
	b.statements(4+r.Intn(5), 0, locals-3)
	b.expression(2)
	b.emit(Ret, 0)
	return &Program{Name: "structured", Params: params, Locals: locals, Code: b.code}
}

func structuredPrograms(seed string) []*Program {
	r := rand.New(rand.NewSource(int64(len(seed))))
	var out []*Program
	for i := range 8 {
		out = append(out, randomStructured(r, 1+i%3, 4+i%5+3))
	}
	return out
}

func TestStructuredProgramsMatchInterpreter(t *testing.T) {
	hostSupported(t)
	r := rand.New(rand.NewSource(2))
	reused := reusedCompilers()
	for i := range 400 {
		p := randomStructured(r, 1+i%3, 3+i%6+3)
		if _, err := p.check(); err != nil {
			t.Fatalf("program %d: %v\n%v", i, err, p.Code)
		}
		for _, args := range [][]uintptr{{0, 0, 0}, {1, 2, 3}, {uintptr(r.Uint64()), uintptr(r.Intn(9)), 7}} {
			args = args[:p.Params]
			want, err := Interpret(p, args...)
			if err != nil {
				t.Fatal(err)
			}
			for k, c := range compilers {
				got, err := RunWith(reused[k], p, args...)
				if err != nil {
					t.Fatalf("%s program %d: %v\n%v", c.name, i, err, p.Code)
				}
				if got != want {
					t.Fatalf("%s program %d %v: compiled %d, interpreter %d\n%v", c.name, i, args, got, want, p.Code)
				}
			}
		}
	}
}

// The batch path keeps loop locals in registers: the squares loop needs no
// frame slots.
func TestBatchKeepsLocalsInRegisters(t *testing.T) {
	for _, arch := range architectures {
		if arch == "386" {
			continue // six registers for three locals, the argument and temporaries
		}
		target, err := newTarget(arch)
		if err != nil {
			t.Fatal(err)
		}
		f, err := (&Compiler{Alloc: &regalloc.Batch{}}).Compile(Programs[0], target, true)
		if err != nil {
			t.Fatal(err)
		}
		if f.FrameBytes != target.frameBytes(0) {
			t.Errorf("%s: squares uses a %d-byte frame:\n%s", arch, f.FrameBytes, f.Listing)
		}
	}
}
