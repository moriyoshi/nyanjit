package compile

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/ir"
)

// programFacts is owned by an immutable source program, never a global cache.
type programFacts = ir.Analysis

// newProgramFacts analyzes a program the compiler built from validated input.
func newProgramFacts(p *ir.Program) *programFacts {
	a, err := ir.Analyze(p)
	if err != nil {
		panic(fmt.Sprintf("compile: lowered program failed analysis: %v", err))
	}
	return a
}

func factsFor(p *ir.Program, facts []*programFacts) *programFacts {
	if len(facts) != 0 && facts[0] != nil {
		return facts[0]
	}
	return newProgramFacts(p)
}
