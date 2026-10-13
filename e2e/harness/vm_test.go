package harness

import (
	"strings"
	"testing"
)

func TestVMConfigMapsBakedImages(t *testing.T) {
	cases := []struct {
		target Target
		name   string
		dir    string
	}{
		{Target{OS: "freebsd", Arch: "386"}, "freebsd-amd64", "freebsd-15.1-amd64"},
		{Target{OS: "freebsd", Arch: "arm"}, "freebsd-arm64tcg", "freebsd-15.1-arm64"},
		{Target{OS: "netbsd", Arch: "386"}, "netbsd-amd64", "netbsd-11.0-amd64"},
		{Target{OS: "netbsd", Arch: "arm"}, "netbsd-arm", "netbsd-11.0-arm"},
		{Target{OS: "openbsd", Arch: "386"}, "openbsd-386", "openbsd-7.9-i386"},
		{Target{OS: "openbsd", Arch: "riscv64"}, "openbsd-riscv64", "openbsd-7.9-riscv64"},
		{Target{OS: "dragonfly", Arch: "amd64"}, "dragonfly-amd64", "dragonfly-6.4.2-amd64"},
		{Target{OS: "solaris", Arch: "amd64"}, "solaris-amd64", "omnios-r151058-amd64"},
		{Target{OS: "illumos", Arch: "amd64"}, "omnios-amd64", "omnios-r151058-amd64"},
	}
	for _, tc := range cases {
		if got := vmName(tc.target); got != tc.name {
			t.Errorf("%s VM name = %q, want %q", tc.target, got, tc.name)
		}
		dir, _, err := vmConfig(tc.name)
		if err != nil || dir != tc.dir {
			t.Errorf("%s directory = %q, %v; want %q", tc.target, dir, err, tc.dir)
		}
	}
}

func TestVMArgsForAArch32Compat(t *testing.T) {
	args, err := vmArgs(vmBoot{Arch: "arm64", Machine: "virt", CPU: "cortex-a72", Firmware: "aarch64-uefi", DiskBus: "virtio", NIC: "virtio-net-pci"},
		"/tmp/overlay.qcow2", t.TempDir(), 22071, true)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"qemu-system-aarch64", "virt,accel=tcg", "-cpu cortex-a72", "hostfwd=tcp:127.0.0.1:22071-:22"} {
		if !strings.Contains(joined, want) {
			t.Errorf("VM argv %q lacks %q", joined, want)
		}
	}
}

func TestNetBSDArmUsesBuildMachine(t *testing.T) {
	args, err := vmArgs(vmBoot{OS: "netbsd", Arch: "arm", Machine: "virt", CPU: "cortex-a15", Firmware: "arm-uefi"},
		"/tmp/overlay.qcow2", t.TempDir(), 22032, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(args, " "); !strings.Contains(got, "virt,highmem=off,accel=tcg") {
		t.Fatalf("NetBSD/arm VM args omit required highmem setting: %s", got)
	}
}

func TestGuestUnameIgnoresSSHNotice(t *testing.T) {
	got := guestUname("Warning: Permanently added '[127.0.0.1]:22003'\r\narm64\n")
	if got != "arm64" {
		t.Fatalf("uname = %q, want arm64", got)
	}
}
