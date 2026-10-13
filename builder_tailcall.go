package nyanjit

// TailCallC ends the current block with a tail call of the native function
// address target through the C ABI, returning its result as this
// function's own; see builder.Builder.TailCallC. Lowering accepts it only
// for a compile.Func with the C ABI in compile.ContextForeign or
// compile.ContextExternal.
func (b *Builder) TailCallC(target Value, args ...Value) {
	if b.ready() {
		b.okDo(b.inner.TailCallC(target, args...))
	}
}
