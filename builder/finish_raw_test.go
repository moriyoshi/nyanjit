package builder

import "github.com/moriyoshi/nyanjit/ir"

// Inspection/mutation fixtures explicitly opt back into editable raw IR.
func testFinishRaw(b interface {
	Finish() (*ir.SealedProgram, error)
}) (*ir.Program, error) {
	p, err := b.Finish()
	if err != nil {
		return nil, err
	}
	return p.Clone(), nil
}
