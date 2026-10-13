package compile

import (
	"fmt"
	"strconv"

	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/ir"
)

func callableGoTargets(b *loweringInput) ([]execution.GoCallTarget, error) {
	var targets []execution.GoCallTarget
	seen := map[execution.GoCallTarget]bool{}
	for n := range b.code {
		ins := &b.code[n]
		if ins.Op != ir.OpCallGo {
			continue
		}
		target := b.code[ins.Args[0]]
		// A native-width cast or an immutable scope capture preserves a fixed
		// target. Mutable Gets and arbitrary arithmetic remain dynamic.
		for steps := 0; steps < len(b.code); steps++ {
			if target.Op == ir.OpCastUnsigned && target.Bits >= uint8(strconv.IntSize) {
				target = b.code[target.A]
				continue
			}
			if target.Op == ir.OpGet {
				v := b.source.Variables[target.Variable]
				if v.Immutable && len(v.Initial) == 1 {
					target = b.code[v.Initial[0]]
					continue
				}
			}
			break
		}
		if target.Op != ir.OpConst {
			return nil, fmt.Errorf("CallGo requires a fixed Go function constant or symbol; dynamic targets are forbidden: %w", ErrInvalidFunction)
		}
		t := execution.GoCallTarget{Address: target.Imm, Symbol: target.Symbol}
		if !seen[t] {
			seen[t] = true
			targets = append(targets, t)
		}
	}
	return targets, nil
}
