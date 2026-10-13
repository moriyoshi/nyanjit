//go:build linux && loong64

package abicheck

import "github.com/moriyoshi/nyanjit/backend/abi"

// The psABI preserves R22–R31 (fp and s0–s8) and F24–F31; ABIInternal fixes
// g (R22). R23 carries the workspace.
var registers = [...]register{
	{name: "R23", c: true},
	{name: "R24", c: true},
	{name: "R25", c: true},
	{name: "R26", c: true},
	{name: "R27", c: true},
	{name: "R28", c: true},
	{name: "R29", c: true},
	{name: "R30", c: true},
	{name: "R31", c: true},
	{name: "F24", c: true},
	{name: "F25", c: true},
	{name: "F26", c: true},
	{name: "F27", c: true},
	{name: "F28", c: true},
	{name: "F29", c: true},
	{name: "F30", c: true},
	{name: "F31", c: true},
	{name: "SP", recorded: true, c: true, goABI: true},
	{name: "g", recorded: true, c: true, goABI: true},
}

const registerSlots = 21

func argumentWord(_ abi.Flavor, i int) (abi.WordLocation, bool) {
	return registerWord([]string{"R4", "R5", "R6", "R7", "R8", "R9", "R10", "R11"}, i)
}

func resultWord(abi.Flavor, int) abi.WordLocation {
	return abi.WordLocation{Storage: abi.Register, Register: "R4"}
}
