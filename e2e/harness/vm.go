package harness

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type vmProcess struct {
	name, key, user string
	port            int
	ready           chan struct{}
	err             error
	cmd             *exec.Cmd
}

type vmBoot struct {
	OS             string `json:"os"`
	Version        string `json:"version"`
	Arch           string `json:"arch"`
	Machine        string `json:"machine"`
	CPU            string `json:"cpu"`
	Firmware       string `json:"firmware"`
	DiskBus        string `json:"disk_bus"`
	NIC            string `json:"nic"`
	SSHUser        string `json:"ssh_user"`
	ExpectedUnameM string `json:"expected_uname_m"`
}

var errNoRunner = errors.New("no runner")

func vmRunnerReason(t Target) string {
	if t.Runner() != "no runner" {
		return ""
	}
	if directory, _, err := vmConfig(vmName(t)); err == nil {
		if data, err := os.ReadFile(filepath.Join("/opt/nyanjit/vms", directory, "NO-RUNNER")); err == nil {
			if line, _, ok := strings.Cut(strings.TrimSpace(string(data)), "\n"); ok {
				return line
			}
			return strings.TrimSpace(string(data))
		}
	}
	return "no baked image for this target"
}

func (b *SystemBackend) getVM(name string) *vmProcess {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.vms[name]
}
func vmConfig(name string) (directory string, port int, err error) {
	parts := strings.Split(name, "-")
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("invalid VM %q", name)
	}
	osname, arch := parts[0], parts[1]
	if arch == "arm64tcg" {
		arch = "arm64"
	}
	if osname == "openbsd" && arch == "386" {
		arch = "i386"
	}
	switch osname {
	case "freebsd":
		directory = "freebsd-15.1-" + arch
		port = 22001
	case "netbsd":
		directory = "netbsd-11.0-" + arch
		port = 22002
	case "openbsd":
		directory = "openbsd-7.9-" + arch
		port = 22003
	case "dragonfly":
		directory = "dragonfly-6.4.2-amd64"
		port = 22004
	case "solaris":
		// GOOS=solaris executes on OmniOS, explicitly labelled as a substitute.
		directory = "omnios-r151058-amd64"
		port = 22005
	case "omnios":
		directory = "omnios-r151058-amd64"
		port = 22006
	default:
		return "", 0, fmt.Errorf("unknown VM OS %q", osname)
	}
	if strings.HasSuffix(name, "arm64tcg") {
		port += 60
	} else if arch == "arm64" {
		port += 10
	}
	if arch == "riscv64" {
		port += 20
	}
	if arch == "arm" {
		port += 30
	}
	if arch == "ppc64" {
		port += 40
	}
	if arch == "386" || arch == "i386" {
		port += 50
	}
	return
}
func (b *SystemBackend) vmUp(ctx context.Context, name string) error {
	b.mu.Lock()
	if b.vms == nil {
		b.vms = map[string]*vmProcess{}
	}
	if v := b.vms[name]; v != nil {
		b.mu.Unlock()
		select {
		case <-v.ready:
			return v.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	v := &vmProcess{name: name, user: "root", ready: make(chan struct{})}
	b.vms[name] = v
	b.mu.Unlock()
	v.err = b.startVM(ctx, v)
	if v.err != nil && v.cmd != nil && v.cmd.Process != nil {
		_ = v.cmd.Process.Kill()
		_ = v.cmd.Wait()
	}
	close(v.ready)
	return v.err
}
func (b *SystemBackend) startVM(ctx context.Context, v *vmProcess) error {
	directory, port, e := vmConfig(v.name)
	if e != nil {
		return e
	}
	v.port = port
	image := filepath.Join("/opt/nyanjit/vms", directory)
	if reason, err := os.ReadFile(filepath.Join(image, "NO-RUNNER")); err == nil {
		return fmt.Errorf("%w: %s: %s", errNoRunner, v.name, strings.TrimSpace(string(reason)))
	}
	data, err := os.ReadFile(filepath.Join(image, "boot.json"))
	if err != nil {
		return fmt.Errorf("VM %s has no baked boot configuration: %w", v.name, err)
	}
	var boot vmBoot
	if err := json.Unmarshal(data, &boot); err != nil {
		return fmt.Errorf("VM %s boot configuration: %w", v.name, err)
	}
	publicKey, err := os.ReadFile(filepath.Join(image, "id_ed25519.pub"))
	if err != nil {
		return fmt.Errorf("VM %s public key: %w", v.name, err)
	}
	// A rebuilt image has a new keypair. Give it a fresh overlay so an old
	// cached overlay cannot silently shadow its new backing disk.
	keyHash := sha256.Sum256(publicKey)
	state := filepath.Join("/cache/vms", fmt.Sprintf("%s-%x", v.name, keyHash[:8]))
	if err := os.MkdirAll(state, 0700); err != nil {
		return err
	}
	backing := filepath.Join(image, "disk.qcow2")
	overlay := filepath.Join(state, "overlay.qcow2")
	if _, err := os.Stat(overlay); os.IsNotExist(err) {
		cmd := exec.CommandContext(ctx, "qemu-img", "create", "-f", "qcow2", "-F", "qcow2", "-b", backing, overlay)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("VM %s overlay: %w: %s", v.name, err, output)
		}
	} else if err != nil {
		return err
	}
	v.key = filepath.Join(state, "ssh.conf")
	v.user = boot.SSHUser
	if v.user == "" {
		v.user = "root"
	}
	config := fmt.Sprintf("Host %s\n HostName 127.0.0.1\n Port %d\n User %s\n IdentityFile %s\n StrictHostKeyChecking no\n UserKnownHostsFile /dev/null\n LogLevel ERROR\n BatchMode yes\n ConnectTimeout 5\n", v.name, port, v.user, filepath.Join(image, "id_ed25519"))
	if err := os.WriteFile(v.key, []byte(config), 0600); err != nil {
		return err
	}
	argv, err := vmArgs(boot, overlay, state, port, strings.HasSuffix(v.name, "arm64tcg"))
	if err != nil {
		return err
	}
	serial, err := os.OpenFile(filepath.Join(state, "serial.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer serial.Close()
	v.cmd = exec.Command(argv[0], argv[1:]...)
	v.cmd.Stdin = nil
	v.cmd.Stdout = serial
	v.cmd.Stderr = serial
	if err := v.cmd.Start(); err != nil {
		return fmt.Errorf("VM %s QEMU: %w", v.name, err)
	}
	ssh := []string{"ssh", "-F", v.key, v.name, "uname -m"}
	deadline := time.NewTimer(15 * time.Minute)
	defer deadline.Stop()
	for {
		p := runCommand(ctx, b.Root, nil, ssh)
		if p.ExitCode == 0 {
			actual := guestUname(p.Output)
			if boot.ExpectedUnameM != "" && actual != boot.ExpectedUnameM {
				return fmt.Errorf("VM %s uname -m = %q, expected %q", v.name, actual, boot.ExpectedUnameM)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("VM %s SSH readiness timed out: %s", v.name, tail(p.Output, 300))
		case <-time.After(3 * time.Second):
		}
	}
}

func guestUname(output string) string {
	output = strings.TrimSpace(output)
	if i := strings.LastIndexByte(output, '\n'); i >= 0 {
		output = strings.TrimSpace(output[i+1:])
	}
	return output
}

func vmArgs(boot vmBoot, disk, state string, port int, forceTCG bool) ([]string, error) {
	arch := boot.Arch
	var exe string
	switch arch {
	case "amd64":
		exe = "qemu-system-x86_64"
	case "386", "i386":
		exe = "qemu-system-i386"
	case "arm64":
		exe = "qemu-system-aarch64"
	case "arm":
		exe = "qemu-system-arm"
	case "riscv64":
		exe = "qemu-system-riscv64"
	case "ppc64":
		exe = "qemu-system-ppc64"
	default:
		return nil, fmt.Errorf("unknown VM architecture %q", arch)
	}
	accel := "tcg"
	if !forceTCG && ((arch == "arm64" && runtime.GOARCH == "arm64") || (arch == "amd64" && runtime.GOARCH == "amd64")) {
		if _, err := os.Stat("/dev/kvm"); err == nil {
			accel = "kvm"
		}
	}
	machine := boot.Machine
	if machine == "" {
		machine = "virt"
	}
	if boot.OS == "netbsd" && arch == "arm" && machine == "virt" {
		machine += ",highmem=off"
	}
	cpu := boot.CPU
	if cpu == "" {
		cpu = "max"
	}
	if forceTCG {
		cpu = "cortex-a72"
	} else if accel == "kvm" && arch == "arm64" {
		cpu = "host"
	}
	argv := []string{exe, "-M", machine + ",accel=" + accel, "-cpu", cpu,
		"-m", "3072", "-smp", "2", "-display", "none", "-monitor", "none", "-serial", "stdio"}
	switch boot.Firmware {
	case "aarch64-uefi":
		argv = append(argv, "-bios", "/usr/share/qemu-efi-aarch64/QEMU_EFI.fd")
	case "arm-uefi":
		argv = append(argv, "-bios", "/usr/share/AAVMF/AAVMF32_CODE.fd")
	case "opensbi-uboot":
		argv = append(argv, "-bios", "/usr/share/qemu/opensbi-riscv64-generic-fw_dynamic.bin",
			"-kernel", "/usr/lib/u-boot/qemu-riscv64_smode/u-boot.bin")
	case "ovmf":
		vars := filepath.Join(state, "ovmf-vars.fd")
		if _, err := os.Stat(vars); os.IsNotExist(err) {
			data, err := os.ReadFile("/usr/share/OVMF/OVMF_VARS_4M.fd")
			if err != nil {
				return nil, err
			}
			if err := os.WriteFile(vars, data, 0600); err != nil {
				return nil, err
			}
		}
		argv = append(argv, "-drive", "if=pflash,format=raw,readonly=on,file=/usr/share/OVMF/OVMF_CODE_4M.fd",
			"-drive", "if=pflash,format=raw,file="+vars)
	case "bios", "":
	default:
		return nil, fmt.Errorf("unknown VM firmware %q", boot.Firmware)
	}
	bus := boot.DiskBus
	if bus == "" {
		bus = "virtio"
	}
	argv = append(argv, "-drive", fmt.Sprintf("file=%s,if=%s,format=qcow2", disk, bus),
		"-netdev", fmt.Sprintf("user,id=net0,hostfwd=tcp:127.0.0.1:%d-:22", port))
	nic := boot.NIC
	if nic == "" {
		nic = "virtio-net-pci"
	}
	argv = append(argv, "-device", nic+",netdev=net0")
	return argv, nil
}
func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}
func (b *SystemBackend) vmDown(ctx context.Context, name string) error {
	v := b.getVM(name)
	if v == nil {
		return nil
	}
	select {
	case <-v.ready:
	case <-ctx.Done():
		return ctx.Err()
	}
	if v.err != nil {
		return v.err
	}
	shutdown := "/sbin/shutdown -p now"
	if strings.HasPrefix(v.name, "omnios-") || strings.HasPrefix(v.name, "solaris-") {
		shutdown = "/usr/sbin/shutdown -y -g0 -i5"
	}
	cmd := exec.CommandContext(ctx, "ssh", "-F", v.key, v.name, shutdown)
	output, shutdownErr := cmd.CombinedOutput()
	if shutdownErr != nil {
		fmt.Fprintf(os.Stderr, "VM %s shutdown command failed: %v: %s\n", name, shutdownErr, strings.TrimSpace(string(output)))
	}
	if v.cmd != nil && v.cmd.Process != nil {
		done := make(chan struct{})
		go func() { _ = v.cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(90 * time.Second):
			_ = v.cmd.Process.Kill()
			<-done
		}
	}
	b.mu.Lock()
	delete(b.vms, name)
	b.mu.Unlock()
	return nil
}

func (b *SystemBackend) closeVMs() {
	b.mu.Lock()
	names := make([]string, 0, len(b.vms))
	for name := range b.vms {
		names = append(names, name)
	}
	b.mu.Unlock()
	var wg sync.WaitGroup
	for _, name := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			_ = b.vmDown(ctx, name)
		}()
	}
	wg.Wait()
}
