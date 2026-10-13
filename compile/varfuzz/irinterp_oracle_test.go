//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || loong64 || 386)

package varfuzz

import (
	"github.com/moriyoshi/nyanjit/instrument/irinterp"
	"github.com/moriyoshi/nyanjit/ir"
)

// interpretIR runs the built program in the reference IR interpreter. Every
// varfuzz callee computes calleeFormula, so the call hook does too.
func interpretIR(prog *ir.SealedProgram, params [3]uint64) (irinterp.Result, error) {
	options := &irinterp.Options{Call: func(c irinterp.Call) (uint64, error) { return calleeFormula(c.Args), nil }}
	return irinterp.Run(prog, params[:prog.Arity()], options)
}
