package cell

import "github.com/moriyoshi/nyanjit"

// Cell is a mutable uint64 value with a caller-provided native-memory home.
// Until Address is called, the home must not be accessed through another
// alias: the current value may exist only as a virtual value. The home must
// remain valid and outside Go-managed memory while generated code uses it.
type Cell struct {
	builder *nyanjit.Builder
	home    nyanjit.Value
	current nyanjit.Value
	exposed bool
}

// New creates a cell whose addressable home is home. The initial value
// remains virtual until Address first exposes the home. New does not
// allocate memory or write to home.
func New(b *nyanjit.Builder, home, initial nyanjit.Value) (*Cell, error) {
	if b == nil || !b.Valid(home) || !b.Valid(initial) {
		return nil, nyanjit.ErrInvalidFunction
	}
	return &Cell{builder: b, home: home, current: initial}, nil
}

// Load returns the current cell value. After its address is exposed, Load
// reads the home each time so writes through aliases are observed.
func (c *Cell) Load() (nyanjit.Value, error) {
	if c == nil || c.builder == nil || !c.builder.Valid(c.home) {
		return nyanjit.Value{}, nyanjit.ErrInvalidFunction
	}
	if c.exposed {
		return c.builder.Load64(c.home), nil
	}
	if !c.builder.Valid(c.current) {
		return nyanjit.Value{}, nyanjit.ErrInvalidFunction
	}
	return c.current, nil
}

// Store updates the cell. Before address exposure it updates only the virtual
// value; afterward it writes the home immediately.
func (c *Cell) Store(value nyanjit.Value) error {
	if c == nil || c.builder == nil || !c.builder.Valid(c.home) || !c.builder.Valid(value) {
		return nyanjit.ErrInvalidFunction
	}
	if c.exposed {
		c.builder.Store64(c.home, value)
	}
	c.current = value
	return nil
}

// Address materializes the current value in the home on first use, then
// returns the caller-provided address. Later aliases may change that memory.
func (c *Cell) Address() (nyanjit.Value, error) {
	if c == nil || c.builder == nil || !c.builder.Valid(c.home) {
		return nyanjit.Value{}, nyanjit.ErrInvalidFunction
	}
	if !c.exposed {
		if !c.builder.Valid(c.current) {
			return nyanjit.Value{}, nyanjit.ErrInvalidFunction
		}
		c.builder.Store64(c.home, c.current)
		c.exposed = true
	}
	return c.home, nil
}
