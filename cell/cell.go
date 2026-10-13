package cell

import (
	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/compile"
)

// Cell is a mutable uint64 kept in a caller-provided native-memory home. New
// writes the initial value, Store writes the home and Load reads it, so the
// home holds the current value on every control-flow path and other aliases
// may access it at any time. The home must remain valid and outside Go-managed
// memory while generated code uses it.
type Cell struct {
	builder *nyanjit.Builder
	home    nyanjit.Value
}

// New creates a cell whose home is home and writes initial to it. New does not
// allocate memory.
func New(b *nyanjit.Builder, home, initial nyanjit.Value) (*Cell, error) {
	if b == nil || !b.Valid(home) || !b.Valid(initial) {
		return nil, compile.ErrInvalidFunction
	}
	b.Store64(home, initial)
	return &Cell{builder: b, home: home}, nil
}

// Load reads the home, observing writes through aliases.
func (c *Cell) Load() (nyanjit.Value, error) {
	if err := c.check(); err != nil {
		return nyanjit.Value{}, err
	}
	return c.builder.Load64(c.home), nil
}

// Store writes value to the home.
func (c *Cell) Store(value nyanjit.Value) error {
	if err := c.check(); err != nil {
		return err
	}
	if !c.builder.Valid(value) {
		return compile.ErrInvalidFunction
	}
	c.builder.Store64(c.home, value)
	return nil
}

// Address returns the caller-provided home address.
func (c *Cell) Address() (nyanjit.Value, error) {
	if err := c.check(); err != nil {
		return nyanjit.Value{}, err
	}
	return c.home, nil
}

func (c *Cell) check() error {
	if c == nil || c.builder == nil || !c.builder.Valid(c.home) {
		return compile.ErrInvalidFunction
	}
	return nil
}
