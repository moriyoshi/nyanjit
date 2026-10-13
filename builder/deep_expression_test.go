package builder_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/builder"
)

// Sharing both inputs must not turn validation into an exponential tree walk,
// and materialization must emit a shared expression only once.
func TestDeepSharedValueExpression(t *testing.T) {
	b := builder.NewBuilder()
	v, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	const depth = 4000
	for i := 0; i < depth; i++ {
		v = v.Xor(v)
	}
	if !b.Valid(v) {
		t.Fatal("shared expression is invalid")
	}
	if err := b.Return(v); err != nil {
		t.Fatal(err)
	}
	p, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(p.Instructions); got != depth+1 {
		t.Fatalf("shared nodes duplicated: %d instructions, want%d", got, depth+1)
	}
}
