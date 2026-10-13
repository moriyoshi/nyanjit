package builder

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/ir"
)

// builderTailCall ends a block with ir.TermTailCall.
const builderTailCall = builderTerm(ir.TermTailCall)

// TailCallC ends the current block by calling the native function address
// target with args through the platform C integer ABI and returning its
// uint64 result as this function's own. The callee reuses this function's
// frame, so a chain of tail calls runs in constant stack. Arguments follow
// CallC's width and extension rules and must fit in argument registers; on
// 386, where C arguments are stack words, the callee may take at most as
// many words as this function's own parameters. Floating-point arguments
// are not supported.
//
// Lowering accepts a tail call only for a Func with the C ABI in
// compile.ContextForeign or compile.ContextExternal; other contexts reject
// it with compile.ErrUnsupported. Like Return, it must be called at the
// root scope.
func (b *Builder) TailCallC(target Value, args ...Value) error {
	if err := requireScalars("TailCallC", append([]Value{target}, args...)...); err != nil {
		return err
	}
	if !b.canAppend() || len(b.scopes) != 1 {
		return fmt.Errorf("TailCallC: current block is closed or nested scope is still open: %w", ErrInvalidFunction)
	}
	var err error
	target, err = b.materialize(target)
	if err != nil {
		return err
	}
	args, err = b.materializeValues(args)
	if err != nil {
		return err
	}
	if !b.valid(target) || target.Type().LaneBits != NativeBits {
		return fmt.Errorf("TailCallC: target value %d has width %d (want native width): %w", target.id, target.Type().LaneBits, ErrInvalidFunction)
	}
	if len(args) > backendMaxArgs {
		return newLimitError("arguments", len(args), backendMaxArgs, "tail call", "tail call has %d arguments, limit %d", len(args), backendMaxArgs)
	}
	ids := make([]int, 1, len(args)+1)
	ids[0] = target.id
	for _, arg := range args {
		if !b.valid(arg) || arg.Type().LaneBits > 64 {
			return fmt.Errorf("TailCallC: argument value %d is invalid or wider than 64 bits: %w", arg.id, ErrInvalidFunction)
		}
		ids = append(ids, arg.id)
	}
	if b.control == nil {
		// A terminator needs a control program. newLabel creates one with
		// the implicit entry label; the label it adds is dropped again.
		if b.newLabel() < 0 {
			return fmt.Errorf("TailCallC: %w", ErrInvalidFunction)
		}
		b.dropLastLabel()
	}
	if s := b.seg; s != nil {
		s.narrowReturn = true
		for _, id := range ids {
			s.use(id, -1)
		}
	}
	_, err = b.endControl(controlEnd{term: builderTailCall, result: -1, args: ids, to: -1, other: -1})
	return err
}
