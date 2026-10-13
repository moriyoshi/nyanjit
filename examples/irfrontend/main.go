// Command irfrontend translates a small stack-machine bytecode into NyanJIT IR
// with ir.ProgramBuilder, compiles it and runs it.
//
// The translation is SSA style. Each bytecode block becomes an IR block whose
// parameters carry the local variables, so a jump passes the locals as edge
// arguments, the way an SSA front end passes phi operands. Function arguments
// are defined once in the entry block, which dominates every block, so every
// block uses them directly without passing them along.
package main

import (
	"fmt"
	"log"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// Bytecode operations. The stack must be empty at every jump and jump target.
const (
	opArg    = iota // push argument N
	opPush          // push constant N
	opLoad          // push local N
	opStore         // pop into local N
	opAdd           // pop b, pop a, push a+b
	opSub           // pop b, pop a, push a-b
	opMul           // pop b, pop a, push a*b
	opJump          // jump to N
	opJumpLT        // pop b, pop a, jump to N if a < b (unsigned)
	opReturn        // pop and return
)

type insn struct {
	op int
	n  uint64
}

type function struct {
	args, locals int
	code         []insn
}

// powerSum returns the sum of i**k for i in [1, n]: arguments n and k.
var powerSum = function{args: 2, locals: 4, code: []insn{
	{opPush, 0}, {opStore, 0}, // sum = 0
	{opPush, 1}, {opStore, 1}, // i = 1
	// outer (4): if n < i, return sum
	{opArg, 0}, {opLoad, 1}, {opJumpLT, 23},
	{opPush, 1}, {opStore, 2}, // p = 1
	{opPush, 0}, {opStore, 3}, // j = 0
	// inner (11): if j < k { p *= i; j++; loop }
	{opLoad, 3}, {opArg, 1}, {opJumpLT, 26},
	{opLoad, 0}, {opLoad, 2}, {opAdd, 0}, {opStore, 0}, // sum += p
	{opLoad, 1}, {opPush, 1}, {opAdd, 0}, {opStore, 1}, // i++
	{opJump, 4},
	// 23: return sum
	{opLoad, 0}, {opReturn, 0}, {opJump, 4}, // unreachable; Seal drops its block
	// 26: p *= i; j++
	{opLoad, 2}, {opLoad, 1}, {opMul, 0}, {opStore, 2},
	{opLoad, 3}, {opPush, 1}, {opAdd, 0}, {opStore, 3},
	{opJump, 11},
}}

// translate emits f as SSA-style IR.
func translate(f function) (*ir.SealedProgram, error) {
	word := ir.Scalar(64)
	// Block leaders: the entry, every jump target, and every instruction
	// after a jump or return.
	leader := map[int]bool{0: true}
	for pc, in := range f.code {
		switch in.op {
		case opJump, opJumpLT:
			leader[int(in.n)] = true
			leader[pc+1] = true
		case opReturn:
			leader[pc+1] = true
		}
	}
	pb := ir.NewProgramBuilder()
	entry := pb.Entry()
	args := make([]int, f.args)
	for i := range args {
		args[i] = pb.Param(entry, word)
	}
	localTypes := make([]ir.Type, f.locals)
	for i := range localTypes {
		localTypes[i] = word
	}
	blocks := map[int]ir.Block{}
	for pc := range leader {
		if pc > 0 && pc < len(f.code) {
			blocks[pc] = pb.NewBlock(localTypes...)
		}
	}
	// The entry block starts with every local zero.
	zero := pb.Append(entry, ir.Instruction{Op: ir.OpConst})
	locals := make([]int, f.locals)
	for i := range locals {
		locals[i] = zero
	}
	block := entry
	var stack []int
	pop := func() int {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		return v
	}
	// jumpTo passes the current locals to the block at pc.
	edge := func(pc int) (ir.Block, []int) {
		return blocks[pc], append([]int(nil), locals...)
	}
	open := true
	for pc, in := range f.code {
		if pc > 0 && leader[pc] {
			if open {
				to, values := edge(pc) // fall through
				pb.Jump(block, to, values...)
			}
			block, open = blocks[pc], true
			for i := range locals {
				locals[i] = pb.BlockParam(block, i)
			}
		}
		if !open {
			continue
		}
		switch in.op {
		case opArg:
			stack = append(stack, args[in.n]) // defined in the dominating entry block
		case opPush:
			stack = append(stack, pb.Append(block, ir.Instruction{Op: ir.OpConst, Imm: in.n}))
		case opLoad:
			stack = append(stack, locals[in.n])
		case opStore:
			locals[in.n] = pop()
		case opAdd, opSub, opMul:
			b, a := pop(), pop()
			op := map[int]ir.Opcode{opAdd: ir.OpAdd, opSub: ir.OpSub, opMul: ir.OpMul}[in.op]
			stack = append(stack, pb.Append(block, ir.Instruction{Op: op, A: a, B: b}))
		case opJump:
			to, values := edge(int(in.n))
			pb.Jump(block, to, values...)
			open = false
		case opJumpLT:
			b, a := pop(), pop()
			taken, takenValues := edge(int(in.n))
			next, nextValues := edge(pc + 1)
			less := &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: ir.BranchBelow, Value: a, Other: b, WithValue: true}
			pb.Branch(block, less, taken, takenValues, next, nextValues)
			open = false
		case opReturn:
			pb.Return(block, pop())
			open = false
		}
		if !open && len(stack) != 0 {
			return nil, fmt.Errorf("pc %d: stack is not empty at the end of a block", pc)
		}
	}
	if open {
		return nil, fmt.Errorf("the last block falls off the end of the code")
	}
	return pb.Seal()
}

func main() {
	program, err := translate(powerSum)
	if err != nil {
		log.Fatal(err)
	}
	target, err := compile.NewTarget(compile.TargetSpec{})
	if err != nil {
		log.Fatal(err)
	}
	unit, err := compile.Lower(target, program, compile.Func{ABI: abi.C})
	if err != nil {
		log.Fatal(err)
	}
	template, err := unit.Template()
	if err != nil {
		log.Fatal(err)
	}
	code, err := template.Bind(nil)
	if err != nil {
		log.Fatal(err)
	}
	defer code.Close()
	for _, k := range []uint64{1, 2, 3} {
		lo, hi, err := code.CallPair(execution.Words(uint64(10), k)...)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("sum of i**%d for i in 1..10 = %d\n", k, execution.Wide(lo, hi))
	}
}
