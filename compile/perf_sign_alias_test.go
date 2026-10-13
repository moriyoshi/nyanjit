//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"github.com/moriyoshi/nyanjit/instrument/workload"
	"github.com/moriyoshi/nyanjit/ir"
)

// signAliasPrefix is the length of signAliases' flag-free prefix per tail
// instruction.
const signAliasPrefix = 64

// signAliases branches on the sign of a 32-bit addition after n explicit
// sign extensions, each an alias of the addition's flags, behind a prefix of
// 64n sign extensions that define no flags the branch reads. Filtering the
// aliases by producer must not rescan the prefix for each alias.
func signAliases(n int) *ir.SealedProgram {
	pb := ir.NewProgramBuilder()
	entry, yes, no := pb.Entry(), pb.NewBlock(), pb.NewBlock()
	a, c := pb.Param(entry, ir.Scalar(32)), pb.Param(entry, ir.Scalar(32))
	for range signAliasPrefix * n {
		a = pb.Append(entry, ir.Instruction{Op: ir.OpSignExtend, Bits: 64, A: a, Imm: 32})
	}
	a = pb.Append(entry, ir.Instruction{Op: ir.OpCastUnsigned, Bits: 32, A: a})
	v := pb.Append(entry, ir.Instruction{Op: ir.OpAdd, Bits: 32, A: a, B: c})
	for range n {
		v = pb.Append(entry, ir.Instruction{Op: ir.OpSignExtend, Bits: 64, A: v, Imm: 32})
	}
	pb.Branch(entry, &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: ir.BranchLess, Value: v}, yes, nil, no, nil)
	pb.Return(yes, pb.Append(yes, ir.Instruction{Op: ir.OpConst, Bits: 64, Imm: 1}))
	pb.Return(no, pb.Append(no, ir.Instruction{Op: ir.OpConst, Bits: 64}))
	program, err := pb.Seal()
	if err != nil {
		panic(err)
	}
	return program
}

// signAliasWorkload is benchmarked but not in the scaling gate: lowering one
// long block already grows about 2.1x per doubling here, too close to the
// gate's limit. TestRegisterImageFlagsScaling gates the alias filter itself.
var signAliasWorkload = workload.Workload{Name: "sign-aliases", Build: signAliases, Size: 1000}
