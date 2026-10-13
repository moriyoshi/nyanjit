package main

import "testing"

func divisionPrograms() []*Program {
	pressure := []Insn{}
	for i := 1; i <= 16; i++ {
		pressure = append(pressure, Insn{Get, 2}, Insn{Push, uintptr(i)}, Insn{Add, 0})
	}
	pressure = append(pressure, Insn{Get, 0}, Insn{Get, 1}, Insn{Div, 0})
	for range 16 {
		pressure = append(pressure, Insn{Add, 0})
	}
	pressure = append(pressure, Insn{Ret, 0})
	return []*Program{
		{Name: "divide", Params: 3, Locals: 3, Code: []Insn{{Get, 0}, {Get, 1}, {Div, 0}, {Ret, 0}}},
		{Name: "divide self", Params: 3, Locals: 3, Code: []Insn{{Get, 0}, {Dup, 0}, {Div, 0}, {Ret, 0}}},
		{Name: "divide constant", Params: 3, Locals: 3, Code: []Insn{{Get, 0}, {Push, 7}, {Div, 0}, {Ret, 0}}},
		{Name: "divide zero", Params: 3, Locals: 3, Code: []Insn{{Get, 0}, {Push, 0}, {Div, 0}, {Ret, 0}}},
		{Name: "divide constant dividend", Params: 3, Locals: 3, Code: []Insn{{Push, 29}, {Get, 1}, {Div, 0}, {Ret, 0}}},
		{Name: "divide with live values", Params: 3, Locals: 3, Code: pressure},
	}
}

func TestDivisionMatchesInterpreter(t *testing.T) {
	hostSupported(t)
	max := ^uintptr(0)
	for _, p := range divisionPrograms() {
		t.Run(p.Name, func(t *testing.T) {
			for _, pair := range [][2]uintptr{{29, 7}, {29, 0}, {0, 0}, {0, 1}, {max, 1}, {max, 2}, {max, max}, {max >> 1, max}, {max>>1 + 1, 3}} {
				args := []uintptr{pair[0], pair[1], max - 17}
				want, err := Interpret(p, args...)
				if err != nil {
					t.Fatal(err)
				}
				if p.Name == "divide" {
					quotient := uintptr(0)
					if pair[1] != 0 {
						quotient = pair[0] / pair[1]
					}
					if want != quotient {
						t.Fatalf("interpreter %v = %d, want %d", pair, want, quotient)
					}
				}
				for _, c := range compilers {
					got, err := RunWith(c.new(), p, args...)
					if err != nil {
						t.Fatalf("%s %v: %v", c.name, pair, err)
					}
					if got != want {
						t.Errorf("%s %v = %d, want %d", c.name, pair, got, want)
					}
				}
			}
		})
	}
}
