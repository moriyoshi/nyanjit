package abi

import "testing"

func TestResolvedCPUArchitecture(t *testing.T) {
	var zero ResolvedCPU
	if zero.Architecture() != "" || zero.Level() != "" || zero.Supports(zero) {
		t.Fatal("zero CPU must not be a validated contract")
	}
	rv, err := (CPUProfile{}).Resolved("riscv64")
	if err != nil {
		t.Fatal(err)
	}
	loong, err := (CPUProfile{}).Resolved("loong64")
	if err != nil {
		t.Fatal(err)
	}
	if rv.Architecture() != "riscv64" || rv.Supports(loong) || loong.Supports(rv) || !rv.Supports(rv) {
		t.Fatal("architecture lost in resolved contract")
	}
}

func TestExtensionBitTableRoundTrip(t *testing.T) {
	// Round-trip every known feature through applicable target validators.
	// resolveCached also panics if an allowed name has no bit-table entry.
	for _, arch := range []string{"386", "amd64", "arm64", "riscv64", "loong64", "ppc64le"} {
		for _, name := range cpuExtensionNames {
			request := CPUProfile{Extensions: []string{name}}
			if arch == "ppc64le" {
				request.Level = "power10"
			}
			profile, err := request.resolve(arch)
			if err != nil {
				continue
			}
			for _, extension := range profile.Extensions {
				n, ok := extensionBit(extension)
				if !ok || int(n) >= len(cpuExtensionNames) || cpuExtensionNames[n] != extension {
					t.Errorf("%s: extension %q missing or mismatched", arch, extension)
				}
			}
		}
	}
}
