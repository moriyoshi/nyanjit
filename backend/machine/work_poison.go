//go:build nyanjit_scratch_poison

package machine

const workspacePoison = true

type layoutBorrowState struct{ instructions bool }

func (b *layoutBorrowState) mark() { b.instructions = true }
func (b *layoutBorrowState) take() bool {
	borrowed := b.instructions
	b.instructions = false
	return borrowed
}
