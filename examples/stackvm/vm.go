package main

import "fmt"

// Op is a stack bytecode operation. Values are native machine words.
type Op uint8

const (
	Push Op = iota // push Arg
	Get            // push local Arg
	Set            // pop into local Arg
	Dup            // push a copy of the top value
	Drop           // pop and discard
	Add            // pop b, pop a, push a+b
	Sub            // pop b, pop a, push a-b
	Mul            // pop b, pop a, push a*b
	And            // pop b, pop a, push a&b
	Or             // pop b, pop a, push a|b
	Xor            // pop b, pop a, push a^b
	Jz             // pop; jump to Arg when it is zero
	Jmp            // jump to Arg
	Ret            // pop and return
)

// Insn is one bytecode instruction. Jump targets are instruction indices.
type Insn struct {
	Op  Op
	Arg uintptr
}

// Program is a function body. The first Params locals hold the arguments
// and the others start at zero. The value stack must be empty at every jump
// and jump target.
type Program struct {
	Name   string
	Params int
	Locals int
	Code   []Insn
}

// check validates stack depths and returns the deepest stack.
func (p *Program) check() (int, error) {
	if p.Params < 0 || p.Locals < p.Params {
		return 0, fmt.Errorf("%s: %d params need at least as many locals, have %d", p.Name, p.Params, p.Locals)
	}
	targets := make([]bool, len(p.Code))
	for _, ins := range p.Code {
		if ins.Op == Jz || ins.Op == Jmp {
			if ins.Arg >= uintptr(len(p.Code)) {
				return 0, fmt.Errorf("%s: jump to %d is outside the program", p.Name, ins.Arg)
			}
			targets[ins.Arg] = true
		}
	}
	depth, deepest := 0, 0
	for pc, ins := range p.Code {
		if targets[pc] && depth != 0 {
			return 0, fmt.Errorf("%s: stack is not empty at jump target %d", p.Name, pc)
		}
		pops, pushes := 0, 0
		switch ins.Op {
		case Push, Get:
			pushes = 1
			if ins.Op == Get && ins.Arg >= uintptr(p.Locals) {
				return 0, fmt.Errorf("%s: local %d at %d does not exist", p.Name, ins.Arg, pc)
			}
		case Set:
			pops = 1
			if ins.Arg >= uintptr(p.Locals) {
				return 0, fmt.Errorf("%s: local %d at %d does not exist", p.Name, ins.Arg, pc)
			}
		case Dup:
			pops, pushes = 1, 2
		case Drop, Ret, Jz:
			pops = 1
		case Add, Sub, Mul, And, Or, Xor:
			pops, pushes = 2, 1
		case Jmp:
		default:
			return 0, fmt.Errorf("%s: unknown op %d at %d", p.Name, ins.Op, pc)
		}
		if depth < pops {
			return 0, fmt.Errorf("%s: stack underflow at %d", p.Name, pc)
		}
		depth += pushes - pops
		deepest = max(deepest, depth)
		if (ins.Op == Jz || ins.Op == Jmp || ins.Op == Ret) && depth != 0 {
			return 0, fmt.Errorf("%s: stack is not empty after %d", p.Name, pc)
		}
	}
	if n := len(p.Code); n == 0 || p.Code[n-1].Op != Ret && p.Code[n-1].Op != Jmp {
		return 0, fmt.Errorf("%s: the last instruction must be Ret or Jmp", p.Name)
	}
	return deepest, nil
}

// Interpret runs p and is the reference for the compiled code.
func Interpret(p *Program, args ...uintptr) (uintptr, error) {
	if _, err := p.check(); err != nil {
		return 0, err
	}
	if len(args) != p.Params {
		return 0, fmt.Errorf("%s: want %d arguments, have %d", p.Name, p.Params, len(args))
	}
	locals := make([]uintptr, p.Locals)
	copy(locals, args)
	var stack []uintptr
	pop := func() uintptr {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		return v
	}
	for pc := 0; ; {
		ins := p.Code[pc]
		pc++
		switch ins.Op {
		case Push:
			stack = append(stack, ins.Arg)
		case Get:
			stack = append(stack, locals[ins.Arg])
		case Set:
			locals[ins.Arg] = pop()
		case Dup:
			stack = append(stack, stack[len(stack)-1])
		case Drop:
			pop()
		case Add, Sub, Mul, And, Or, Xor:
			b, a := pop(), pop()
			stack = append(stack, map[Op]func(a, b uintptr) uintptr{
				Add: func(a, b uintptr) uintptr { return a + b },
				Sub: func(a, b uintptr) uintptr { return a - b },
				Mul: func(a, b uintptr) uintptr { return a * b },
				And: func(a, b uintptr) uintptr { return a & b },
				Or:  func(a, b uintptr) uintptr { return a | b },
				Xor: func(a, b uintptr) uintptr { return a ^ b },
			}[ins.Op](a, b))
		case Jz:
			if pop() == 0 {
				pc = int(ins.Arg)
			}
		case Jmp:
			pc = int(ins.Arg)
		case Ret:
			return pop(), nil
		}
	}
}
