package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"strings"
	"sync"
	"time"
)

type SystemBackend struct {
	Root      string
	mu        sync.Mutex
	vms       map[string]*vmProcess
	wineLocks map[string]*sync.Mutex
	commandFn func(context.Context, string, []string, []string) ExecResult
	vmRunFn   func(context.Context, Target, string, []string, []string) (ExecResult, error)
}

func qemuName(arch string) string {
	switch arch {
	case "amd64":
		return "qemu-x86_64"
	case "386":
		return "qemu-i386"
	case "arm64":
		return "qemu-aarch64"
	case "loong64":
		return "qemu-loongarch64"
	case "mips64le":
		return "qemu-mips64el"
	default:
		return "qemu-" + arch
	}
}
func qemuCPU(t Target) string {
	switch t.Arch {
	case "amd64":
		return "qemu64"
	case "386":
		return "qemu32,-sse3"
	case "arm":
		switch t.GOARM {
		case "5":
			return "arm926"
		case "6":
			return "arm1176"
		default:
			return "cortex-a7"
		}
	case "arm64":
		return "cortex-a72"
	case "ppc64le":
		if t.GOPPC64 == "" {
			return "power8"
		}
		return t.GOPPC64
	case "loong64":
		return "la464,lsx=off,lasx=off"
	case "riscv64":
		return "rv64"
	default:
		return ""
	}
}

// Isolate the ppc64le interpreter and libraries, including child processes
// that use binfmt.
func qemuSysroot(t Target) string {
	if t.OS == "linux" && (t.Arch == "ppc64le" || t.Arch == "loong64") {
		return "/opt/nyanjit/sysroots/" + t.Arch
	}
	return ""
}

func (b *SystemBackend) Run(ctx context.Context, t Target, path string, args, env []string) (ExecResult, error) {
	if t.Runner() == "no runner" {
		return ExecResult{Status: "no runner", Reason: vmRunnerReason(t)}, nil
	}
	if t.Runner() == "vm" {
		if b.vmRunFn != nil {
			return b.vmRunFn(ctx, t, path, args, env)
		}
		return b.runVM(ctx, t, path, args, env)
	}
	argv := []string{path}
	e := append([]string(nil), env...)
	switch t.Runner() {
	case "qemu-user":
		q := qemuName(t.Arch)
		preserveArgv0 := true
		if t.Arch == "loong64" {
			switch mode := os.Getenv("NYANJIT_E2E_LOONG64_QEMU"); mode {
			case "", "tonistiigi":
			case "debian":
				q = "/opt/nyanjit/qemu-debian/qemu-loongarch64"
				preserveArgv0 = false
			default:
				return ExecResult{}, errors.New("unknown NYANJIT_E2E_LOONG64_QEMU: " + mode)
			}
		}
		if preserveArgv0 {
			if _, err := os.Stat("/q/" + q); err == nil {
				q = "/q/" + q
			}
		}
		// The private binfmt registration matches the selected emulator's
		// argv convention. Explicit patched-QEMU invocation needs the
		// original argv[0] after the executable; otherwise it consumes the
		// first program argument. Stock QEMU constructs argv[0] itself.
		if os.Getenv("NYANJIT_E2E_BINFMT_READY") == "1" {
			argv = append([]string{path}, args...)
		} else if preserveArgv0 {
			argv = append([]string{q, path, path}, args...)
		} else {
			// Stock QEMU supplies argv[0] itself.
			argv = append([]string{q, path}, args...)
		}
		if prefix := qemuSysroot(t); prefix != "" {
			e = append(e, "QEMU_LD_PREFIX="+prefix)
		}
		explicitCPU := false
		for _, v := range e {
			explicitCPU = explicitCPU || strings.HasPrefix(v, "QEMU_CPU=")
		}
		if cpu := qemuCPU(t); cpu != "" && !explicitCPU {
			e = append(e, "QEMU_CPU="+cpu)
		}
	case "wine":
		b.mu.Lock()
		if b.wineLocks == nil {
			b.wineLocks = make(map[string]*sync.Mutex)
		}
		lock := b.wineLocks[t.Arch]
		if lock == nil {
			lock = &sync.Mutex{}
			b.wineLocks[t.Arch] = lock
		}
		b.mu.Unlock()
		lock.Lock()
		defer lock.Unlock()
		argv = append([]string{"wine", path}, args...)
		prefix := "wine-debian-"
		if runtime.GOARCH == "arm64" {
			prefix = "wine-hangover-11.16-e2e-"
		}
		e = append(e, "WINEDEBUG=-all", "WINEPREFIX=/cache/"+prefix+t.Arch)
	default:
		argv = append(argv, args...)
	}
	command := runCommand
	if b.commandFn != nil {
		command = b.commandFn
	}
	r := command(ctx, b.Root, e, argv)
	if ctx.Err() != nil {
		r.Status = "fail"
		r.Reason = ctx.Err().Error()
	}
	return r, nil
}
func vmName(t Target) string {
	osname := t.OS
	if osname == "illumos" {
		osname = "omnios"
	}
	if osname == "solaris" {
		osname = "solaris"
	}
	arch := t.Arch
	if arch == "386" && (osname == "freebsd" || osname == "netbsd") {
		arch = "amd64"
	}
	if arch == "arm" && osname == "freebsd" {
		arch = "arm64tcg"
	}
	return osname + "-" + arch
}
func vmBinaryPath(t Target, path string) string {
	dir := "/tmp"
	if t.OS == "netbsd" {
		// NetBSD's ARM images mount /tmp as a 32 MiB tmpfs. A full package
		// matrix can exhaust it while copying several test binaries at once.
		dir = "/root"
	}
	return filepath.Join(dir, "nyanjit-e2e-"+t.Key()+"-"+filepath.Base(path))
}
func (b *SystemBackend) VMUp(ctx context.Context, name string) error   { return b.vmUp(ctx, name) }
func (b *SystemBackend) VMDown(ctx context.Context, name string) error { return b.vmDown(ctx, name) }
func (b *SystemBackend) runVM(ctx context.Context, t Target, path string, args, env []string) (ExecResult, error) {
	name := vmName(t)
	if err := b.vmUp(ctx, name); err != nil {
		if errors.Is(err, errNoRunner) {
			return ExecResult{Status: "no runner", ExitCode: -1, Reason: err.Error()}, nil
		}
		return ExecResult{Status: "fail", ExitCode: -1, Reason: err.Error()}, nil
	}
	v := b.getVM(name)
	if v == nil {
		return ExecResult{Status: "fail", ExitCode: -1, Reason: "VM not available"}, nil
	}
	remote := vmBinaryPath(t, path)
	copyCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	scp := []string{"scp", "-F", v.key, path, v.name + ":" + remote}
	cp := runCommand(copyCtx, b.Root, nil, scp)
	if cp.ExitCode != 0 {
		reason := "scp: " + cp.Reason
		if detail := strings.TrimSpace(cp.Output); detail != "" {
			reason += ": " + detail
		}
		return ExecResult{Status: "fail", Reason: reason, Output: cp.Output}, nil
	}
	remoteArgs := []string{remote}
	remoteArgs = append(remoteArgs, args...)
	command := "chmod +x " + shellQuote(remote) + " && env " + shellJoin(env) + " " + shellJoin(remoteArgs)
	cmd := []string{"ssh", "-F", v.key, v.name, command}
	r := runCommand(ctx, b.Root, nil, cmd)
	return r, nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func shellJoin(xs []string) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = shellQuote(x)
	}
	return strings.Join(parts, " ")
}
