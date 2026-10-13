package ir

// maxStackAreaBytes bounds one stack area and the sum of a function's areas.
// Target frame budgets are tighter.
const maxStackAreaBytes = 1 << 16

// StackArea is fixed-size, function-lifetime storage that OpStackAddress
// addresses. It is zeroed when the function is entered.
type StackArea struct {
	Bytes int // 1 … 1<<16
	Align int // 1, 2, 4, 8 or 16
}

func validateStackAreas(areas []StackArea) error {
	total := 0
	for i, area := range areas {
		if area.Bytes < 1 || area.Bytes > maxStackAreaBytes {
			return invalidRule("stack area %d: size %d is not 1 to %d bytes", i, area.Bytes, maxStackAreaBytes)
		}
		switch area.Align {
		case 1, 2, 4, 8, 16:
		default:
			return invalidRule("stack area %d: alignment %d is not 1, 2, 4, 8 or 16", i, area.Align)
		}
		total += area.Bytes
		if total > maxStackAreaBytes {
			return invalidRule("stack areas need %d bytes, more than %d", total, maxStackAreaBytes)
		}
	}
	return nil
}

// StackAreas returns the borrowed, read-only stack area table.
func (p *SealedProgram) StackAreas() []StackArea { return p.program().StackAreas }
