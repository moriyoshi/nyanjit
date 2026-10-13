//go:build linux && riscv64

package abicheck

import "github.com/moriyoshi/nyanjit/backend/abi"

// The psABI preserves s0–s11 (X8, X9, X18–X27) and fs0–fs11 (F8, F9,
// F18–F27) and never allocates gp (X3) or tp (X4); ABIInternal fixes gp, tp
// and g (X27). X9 carries the workspace.
var registers = [...]register{
	{name: "X8", c: true},
	{name: "X9", c: true},
	{name: "X18", c: true},
	{name: "X19", c: true},
	{name: "X20", c: true},
	{name: "X21", c: true},
	{name: "X22", c: true},
	{name: "X23", c: true},
	{name: "X24", c: true},
	{name: "X25", c: true},
	{name: "X26", c: true},
	{name: "F8", c: true},
	{name: "F9", c: true},
	{name: "F18", c: true},
	{name: "F19", c: true},
	{name: "F20", c: true},
	{name: "F21", c: true},
	{name: "F22", c: true},
	{name: "F23", c: true},
	{name: "F24", c: true},
	{name: "F25", c: true},
	{name: "F26", c: true},
	{name: "F27", c: true},
	{name: "SP", recorded: true, c: true, goABI: true},
	{name: "g", recorded: true, c: true, goABI: true},
	{name: "X3", recorded: true, c: true, goABI: true},
	{name: "X4", recorded: true, c: true, goABI: true},
}

const registerSlots = 31

func argumentWord(_ abi.Flavor, i int) (abi.WordLocation, bool) {
	return registerWord([]string{"X10", "X11", "X12", "X13", "X14", "X15", "X16", "X17"}, i)
}

func resultWord(abi.Flavor, int) abi.WordLocation {
	return abi.WordLocation{Storage: abi.Register, Register: "X10"}
}
