//go:build linux && arm64

package abicheck

import "github.com/moriyoshi/nyanjit/backend/abi"

// AAPCS64 preserves X19–X29 and the low halves of V8–V15; ABIInternal fixes
// the reserved X18, the frame pointer X29 and g (X28). X8 carries the
// workspace.
var registers = [...]register{
	{name: "X8"},
	{name: "X18", goABI: true},
	{name: "X19", c: true},
	{name: "X20", c: true},
	{name: "X21", c: true},
	{name: "X22", c: true},
	{name: "X23", c: true},
	{name: "X24", c: true},
	{name: "X25", c: true},
	{name: "X26", c: true},
	{name: "X27", c: true},
	{name: "X29", c: true, goABI: true},
	{name: "V8", c: true},
	{name: "V9", c: true},
	{name: "V10", c: true},
	{name: "V11", c: true},
	{name: "V12", c: true},
	{name: "V13", c: true},
	{name: "V14", c: true},
	{name: "V15", c: true},
	{name: "SP", recorded: true, c: true, goABI: true},
	{name: "g", recorded: true, c: true, goABI: true},
}

const registerSlots = 24

func argumentWord(_ abi.Flavor, i int) (abi.WordLocation, bool) {
	return registerWord([]string{"X0", "X1", "X2", "X3", "X4", "X5", "X6", "X7"}, i)
}

func resultWord(abi.Flavor, int) abi.WordLocation {
	return abi.WordLocation{Storage: abi.Register, Register: "X0"}
}
