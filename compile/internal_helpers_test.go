package compile

import "testing"

// must returns v and panics on err. Internal tests build programs with
// builder.Builder, which reports each operation's error immediately; a
// builder error is a broken fixture, not a compiler result.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// mustDo panics on a builder operation's error.
func mustDo(err error) {
	if err != nil {
		panic(err)
	}
}

// hostBackend reports whether the host architecture has a code generator.
func hostBackend() bool {
	target, err := NewTarget(TargetSpec{})
	return err == nil && newMachineTarget(target.resolved().options(Func{})) != nil
}

// mustTarget resolves spec for tests that inspect the lowering pipeline's
// internal options, such as mustTarget(t, spec).options(f).
func mustTarget(t testing.TB, spec TargetSpec) *resolvedTarget {
	t.Helper()
	target, err := NewTarget(spec)
	if err != nil {
		t.Fatal(err)
	}
	return target.resolved()
}

// uncheckedTarget builds a target for spec without NewTarget's validation, so
// tests can drive internal CPU and vector checks with requests NewTarget
// rejects, such as uncheckedTarget(spec).options(f).
func uncheckedTarget(spec TargetSpec) *resolvedTarget {
	return &resolvedTarget{spec: spec, features: spec.Vectors.features()}
}
