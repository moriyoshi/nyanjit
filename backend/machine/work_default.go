//go:build !nyanjit_scratch_poison

package machine

const workspacePoison = false

// Empty in ordinary builds: borrow tracking costs neither storage nor writes.
type layoutBorrowState struct{}

func (*layoutBorrowState) mark()      {}
func (*layoutBorrowState) take() bool { return false }
