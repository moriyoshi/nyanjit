package compile

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestSelectRejectsScopeLeaveInputsBeforeReachability(t *testing.T) {
	for _, target := range []incSelectionTarget{{arch: "amd64", bits: 64}, {arch: "386", bits: 32}} {
		for _, input := range []int{-1, 1, 2, int(^uint(0) >> 1)} {
			ops := []ir.Instruction{
				{Op: op.OpConst, Imm: 1},
				{Op: op.OpScopeLeave, Args: []int{input}},
			}
			if _, err := testSelectProgram(ops, nil, 0, 0, nil, 0, target); !errors.Is(err, backend.ErrInvalidFunction) {
				t.Fatalf("%s input %d: %v", target.arch, input, err)
			}
		}
	}
}
