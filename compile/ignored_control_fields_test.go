package compile_test

import (
	"fmt"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestIgnoredControlFieldsLegalization(t *testing.T) {
	for _, vector := range []bool{false, true} {
		b := n.NewBuilder()
		pv := b.Var(b.ParamNative())
		ptr := b.Get(pv)
		if vector {
			b.VStore(ptr, b.VLoad(ir.Vec(4, 32), ptr))
		} else {
			b.Store16(ptr, b.Load16(ptr))
		}
		label := b.NewLabel()
		b.Jump(label)
		b.Bind(label)
		b.Return(b.Load64(b.Get(pv)))
		sealed, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		base := sealed.Clone()
		for _, result := range []int{-1, len(base.Instructions), 1 << 20} {
			for _, arch := range predicateFuzzArchs {
				t.Run(fmt.Sprintf("%v/%d/%s", vector, result, arch), func(t *testing.T) {
					p := base.Clone()
					for i := range p.Control.Steps {
						s := &p.Control.Steps[i]
						if s.IsLabel {
							continue
						}
						if s.Term == ir.TermJump {
							s.Result = result
						}

					}
					if err := p.Validate(); err != nil {
						t.Fatal(err)
					}
					spec := compile.TargetSpec{Architecture: arch}
					fn := compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 16}}
					in := func(c compile.Context) compile.Func {
						f := fn
						f.Context = c
						return f
					}
					want, err := lowerListing(base, spec, fn)
					if err != nil {
						t.Fatal(err)
					}
					got, err := lowerListing(p, spec, fn)
					if err != nil || got != want {
						t.Fatalf("ignored fields changed listing: %v", err)
					}
					for _, entry := range []struct {
						name string
						call func() error
					}{
						{"Lower", func() error {
							u, e := lowerUnit(p, spec, fn)
							if e != nil {
								return e
							}
							_, e = u.Listing()
							return e
						}},
						{"Report", func() error { _, e := lowerReport(p, spec, fn); return e }},
						{"Template", func() error { _, e := lowerTemplate(p, spec, fn); return e }},
						{"TemplateGoManaged", func() error { _, e := lowerTemplate(p, spec, in(compile.ContextGoManaged)); return e }},
						{"TemplateForeign", func() error { _, e := lowerTemplate(p, spec, in(compile.ContextForeign)); return e }},
						{"TemplateExternal", func() error { _, e := lowerTemplate(p, spec, in(compile.ContextExternal)); return e }},
						{"RegisterScopes", func() error {
							u, e := lowerUnit(p, spec, fn)
							if e != nil {
								return e
							}
							_, e = u.RegisterScopes()
							return e
						}},
					} {
						if e := entry.call(); predicateFuzzIsValidationErr(e) {
							t.Fatalf("%s validated then invalid: %v", entry.name, e)
						}
					}
				})
			}
		}
		// Explicitly exercise a jump Result naming a side effect.
		p := base.Clone()
		for i := range p.Control.Steps {
			s := &p.Control.Steps[i]
			if !s.IsLabel && s.Term == ir.TermJump {
				for id := s.Start; id < s.End; id++ {
					if !p.Instructions[id].Op.ProducesValue() {
						s.Result = id
					}
				}
			}
		}
		if e := p.Validate(); e != nil {
			t.Fatal(e)
		}
		for _, arch := range predicateFuzzArchs {
			if _, e := lowerListing(p, compile.TargetSpec{Architecture: arch}, compile.Func{}); e != nil {
				t.Fatal(e)
			}
		}
	}
}
