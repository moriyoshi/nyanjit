package compile

import "github.com/moriyoshi/nyanjit/backend/machine"

// prefetch emits OpPrefetch of the address in register address, by the
// ir.PrefetchKind in kind. On a 32-bit target a two-word address uses its
// low word.
func (e *physicalEmitter) prefetch(address physReg, kind uint64) {
	e.append(physicalInsn{Op: machine.OpPrefetch, A: address, Imm: kind})
}
