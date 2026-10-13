//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || 386 || loong64)

package varfuzz

import (
	"github.com/moriyoshi/nyanjit/builder"
)

// Minimized shapes use the same AST, interpreter and emitter as arbitrary
// programs. The four-byte "var" header makes them independently mutable fuzz
// inputs, while execution regressions retain the reviewers' exact Builders.
func minimizedProgram(kind byte) *Program {
	root := &Scope{hashT: &Temp{id: -1, bits: 64}}
	p := &Program{root: root, params: []*Temp{{id: 1, bits: 64}, {id: 2, bits: 32}, {id: 3, bits: builder.NativeBits}}, ntemps: 3}
	add := func(scope *Scope, width uint8, value uint64) *VarD {
		p.nvars++
		v := &VarD{id: p.nvars, bits: width, scope: scope, init: &Expr{op: "const", bits: width, val: value}}
		scope.vars = append(scope.vars, v)
		return v
	}
	p.hash = add(root, 64, 0)
	switch kind % 5 {
	case 0: // workspace base plus a constant comparison at a loop head
		v := add(root, 64, 0)
		v.counter = true
		root.body = []*Stmt{{kind: "loopv", ctr: v, form: 1, n: 3}}
	case 1: // 32-bit AND whose immediate requires a 64-bit arm64 encoding
		v := add(root, 32, 1)
		root.body = []*Stmt{{kind: "if", pk: 3, pa: &Expr{op: "and", bits: 32, a: &Expr{op: "temp", bits: 32, t: p.params[1]}, b: &Expr{op: "const", bits: 32, val: 0xffffffff}}, then: []*Stmt{{kind: "set", v: v, e: &Expr{op: "const", bits: 32, val: 2}}}}}
	case 2: // dirty narrow variable and the 386 wide-multiply scratch registers
		inner := &Scope{depth: 1, hashT: &Temp{id: 4, bits: 64}}
		p.ntemps = 4
		v := add(inner, 8, 7)
		inner.body = []*Stmt{{kind: "set", v: v, e: &Expr{op: "const", bits: 8, val: 9}}, {kind: "set", v: p.hash, e: &Expr{op: "mul", bits: 64, a: &Expr{op: "temp", bits: 64, t: p.params[0]}, b: &Expr{op: "const", bits: 64, val: 17}}}}
		root.body = []*Stmt{{kind: "scope", scope: inner}}
	case 3: // nested Select with constant operands on POWER
		v := add(root, 8, 0)
		z := &Expr{op: "const", bits: 8}
		inner := &Expr{op: "sel", bits: 8, a: z, b: z, c: z}
		root.body = []*Stmt{{kind: "set", v: v, e: &Expr{op: "sel", bits: 8, a: inner, b: z, c: z}}}
	case 4: // managed C-entry allocation differs from ordinary C entry
		for i := 0; i < 13; i++ {
			v := add(root, 8, uint64(i))
			root.body = append(root.body, &Stmt{kind: "set", v: v, e: &Expr{op: "add", bits: 8, a: &Expr{op: "get", bits: 8, v: v}, b: &Expr{op: "const", bits: 8, val: 1}}})
		}
	}
	return p
}
