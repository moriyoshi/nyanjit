package loong64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// floatCompareForms gives, per op.FloatCondition, the fcmp cond field and
// whether the operands are swapped (fj=C, fk=B). Only the quiet C* conditions
// are used, so a quiet NaN raises no invalid exception.
var floatCompareForms = [...]struct {
	cond uint32
	name string
	swap bool
}{
	op.FloatEqual:                 {0x04, "ceq", false},
	op.FloatNotEqual:              {0x18, "cune", false},
	op.FloatLess:                  {0x02, "clt", false},
	op.FloatUnorderedGreaterEqual: {0x0e, "cule", true},
	op.FloatLessEqual:             {0x06, "cle", false},
	op.FloatUnorderedGreater:      {0x0a, "cult", true},
	op.FloatGreater:               {0x02, "clt", true},
	op.FloatUnorderedLessEqual:    {0x0e, "cule", false},
	op.FloatGreaterEqual:          {0x06, "cle", true},
	op.FloatUnorderedLess:         {0x0a, "cult", false},
	op.FloatOrdered:               {0x14, "cor", false},
	op.FloatUnordered:             {0x08, "cun", false},
	op.FloatOrderedNotEqual:       {0x10, "cne", false},
	op.FloatUnorderedEqual:        {0x0c, "cueq", false},
}

func validFloatCompare(c op.FloatCondition, w uint8, d, a, b backend.Register) bool {
	return int(c) < len(floatCompareForms) && (w == 32 || w == 64) && validGeneralRegisters(d) && validVectorRegister(a) && validVectorRegister(b)
}

// FloatCompare writes to d 1 when the w-bit floats in FP registers a and b
// satisfy c, else 0: fcmp.cond into fcc0, then movcf2gr. fcc0 is clobbered;
// no other expansion uses the condition-flag registers, so none is live
// across instructions. Both inputs are read before d is written.
func (e *ByteAssembler) FloatCompare(c op.FloatCondition, w uint8, d, a, b backend.Register) {
	if !e.require(validFloatCompare(c, w, d, a, b)) {
		return
	}
	form := floatCompareForms[c]
	j, k := uint32(backend.VectorNumber(a)), uint32(backend.VectorNumber(b))
	if form.swap {
		j, k = k, j
	}
	code, suffix := uint32(0x0c1<<20), "s"
	if w == 64 {
		code, suffix = 0x0c2<<20, "d"
	}
	e.emit(code|form.cond<<15|k<<10|j<<5, "fcmp.%s.%s fcc0, f%d, f%d", form.name, suffix, j, k)
	e.emit(0x0114dc00|uint32(d), "movcf2gr r%d, fcc0", d)
}
