package backend

// Finalizer releases a host resource that generated code reaches through an
// address compiled into it (for example by unpinning it).
type Finalizer interface{ Finalize() }

// FinalizerFunc adapts a function, e.g. FinalizerFunc(pinner.Unpin).
type FinalizerFunc func()

// Finalize calls f.
func (f FinalizerFunc) Finalize() { f() }

// Finalizers is a list of Finalizers. Finalize runs each once, last
// registered first, as deferred calls do.
type Finalizers []Finalizer

// Finalize runs each Finalizer once, from the last to the first. Nil entries
// are skipped.
func (fs Finalizers) Finalize() {
	for i := len(fs) - 1; i >= 0; i-- {
		if fs[i] != nil {
			fs[i].Finalize()
		}
	}
}
