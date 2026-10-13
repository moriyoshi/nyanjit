package compile_test

import "github.com/moriyoshi/nyanjit/ir"

// Historical selection/validation fixtures inspect and mutate raw snapshots.
// Performance fixtures and immutable-program contract tests use sealed inputs.
func testFinishRaw(b interface {
	Finish() (*ir.SealedProgram, error)
}) (*ir.Program, error) {
	p, err := b.Finish()
	if err != nil {
		return nil, err
	}
	return p.Clone(), nil
}
