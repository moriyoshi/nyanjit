package nyanjit

// Switch ends the block: selector, an unsigned scalar of at most 64 bits,
// below len(cases) continues at cases[selector], and any other value at
// otherwise. Labels may repeat. Like Jump, it leaves no open block; Bind the
// next label. Each case counts as one predicate node, so Switch and Finish
// report an error wrapping compile.ErrBackendLimit above ir.MaxPredicateNodes
// (16,384) cases, or predicate and case nodes in total.
func (b *Builder) Switch(selector Value, cases []Label, otherwise Label) {
	if b.ready() {
		b.okDo(b.inner.Switch(selector, cases, otherwise))
	}
}
