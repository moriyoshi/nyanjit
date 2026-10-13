package i386

import "fmt"

// planCode keeps byte and text assemblers on one expansion plan: the plan
// appends each instruction's code and, for a text assembler, its line.
// Plans format a line only when text is set, so a byte assembler or a size
// query neither formats nor boxes operands.
type planCode struct {
	text  bool
	code  []byte
	lines []string
}

// line records the listing of the instruction just appended.
func (p *planCode) line(format string, args ...any) {
	p.lines = append(p.lines, fmt.Sprintf(format, args...))
}

// recordPlan appends the listing a text plan collected.
func (e *TextAssembler) recordPlan(p *planCode) { e.lines = append(e.lines, p.lines...) }
