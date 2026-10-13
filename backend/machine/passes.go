package machine

// LoopFuser is implemented by targets that rewrite countdown loops into
// dedicated loop instructions. A compiler calls FuseLoops when it prefers
// compact loops, after control-flow compaction and before branch
// relaxation. labels holds each label's byte PC and bound says which labels
// are bound; FuseLoops returns both, possibly with labels added.
type LoopFuser interface {
	FuseLoops(s *Stream, labels []int, bound []bool) ([]int, []bool, error)
}

// FlagElider is implemented by targets whose instructions have forms that
// leave flags alone. ElideDeadFlags selects such a form wherever no path
// reads the flags an instruction writes before they are written again. It
// neither adds nor removes instructions, but a selected form may differ in
// size, so it returns the labels' byte PCs, moved accordingly. A compiler
// calls it after FuseLoops, and on straight-line code with empty labels and
// bound.
type FlagElider interface {
	ElideDeadFlags(s *Stream, labels []int, bound []bool) ([]int, error)
}
