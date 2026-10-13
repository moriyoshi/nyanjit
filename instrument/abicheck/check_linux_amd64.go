//go:build linux && amd64

package abicheck

import "github.com/moriyoshi/nyanjit/backend/abi"

// The System V ABI preserves RBX, RBP and R12–R15; ABIInternal fixes RBP,
// g (R14) and a zero X15. R10 carries the workspace.
var registers = [...]register{
	{name: "RBX", c: true},
	{name: "RBP", c: true, goABI: true},
	{name: "R12", c: true},
	{name: "R13", c: true},
	{name: "R15", c: true},
	{name: "R10"},
	{name: "XMM15", zero: true, goABI: true},
	{name: "SP", recorded: true, c: true, goABI: true},
	{name: "g", recorded: true, c: true, goABI: true},
}

const registerSlots = 11

func argumentWord(flavor abi.Flavor, i int) (abi.WordLocation, bool) {
	if flavor == abi.Go {
		return registerWord([]string{"RAX", "RBX", "RCX", "RDI", "RSI", "R8"}, i)
	}
	return registerWord([]string{"RDI", "RSI", "RDX", "RCX", "R8", "R9"}, i)
}

func resultWord(abi.Flavor, int) abi.WordLocation {
	return abi.WordLocation{Storage: abi.Register, Register: "RAX"}
}
