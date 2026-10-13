package i386

// concreteInstruction keeps byte and text assemblers on one expansion plan.
type concreteInstruction struct {
	code []byte
	text string
}
