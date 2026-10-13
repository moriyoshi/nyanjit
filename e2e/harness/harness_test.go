package harness

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeBackend struct {
	mu     sync.Mutex
	calls  []Target
	result ExecResult
}

func (f *fakeBackend) Run(_ context.Context, t Target, _ string, _ []string, _ []string) (ExecResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, t)
	f.mu.Unlock()
	return f.result, nil
}
func TestE2EScriptsLoad(t *testing.T) {
	root := filepath.Clean("../..")
	for _, script := range []string{"crossbuild.star", "smoke.star", "matrix.star", "ppc64.star", "amd64.star", "arm64.star", "riscv64.star", "loong64.star", "loong64_gc.star", "386.star"} {
		h := New(root, 2, Filter{Suite: "nothing"})
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := h.Run(ctx, filepath.Join(root, "e2e", script))
		cancel()
		if err != nil {
			t.Errorf("%s: %v", script, err)
		}
	}
}
func TestBackendDispatchThroughStarlark(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "test.star")
	src := `def main():
    a = target("linux", "arm64")
    w = target("windows", "amd64")
    p = go(run, a, "/tmp/fake", timeout=2)
    r = run(w, "/tmp/fake", timeout=2)
    assert_eq(join(p, timeout=2).output, "42")
    check(a, "fake", "a", r, contains="42")
` // go() accepts positional arguments; use a wrapper for timeout.
	src = strings.ReplaceAll(src, "go(run, a, \"/tmp/fake\", timeout=2)", "go(run_one, a)")
	src = "def run_one(t):\n    return run(t, \"/tmp/fake\", timeout=2)\n" + src
	if err := os.WriteFile(script, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	f := &fakeBackend{result: ExecResult{Output: "42", Status: "pass"}}
	h := New(root, 2, Filter{})
	h.Backend = f
	if err := h.Run(context.Background(), script); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 2 {
		t.Fatalf("calls=%d", len(f.calls))
	}
	if len(h.Results()) != 1 || h.Results()[0].Status != "pass" {
		t.Fatalf("results=%v", h.Results())
	}
}
func TestRunnerMatrix(t *testing.T) {
	cases := []struct {
		t    Target
		want string
	}{{Target{OS: "linux", Arch: runtime.GOARCH}, "native"}, {Target{OS: "windows", Arch: "386"}, "wine"}, {Target{OS: "freebsd", Arch: "amd64"}, "vm"}, {Target{OS: "openbsd", Arch: "ppc64"}, "no runner"}}
	for _, c := range cases {
		if got := c.t.Runner(); got != c.want {
			t.Errorf("%s: %s want %s", c.t, got, c.want)
		}
	}
}
func TestPromiseDeadline(t *testing.T) {
	p := newPromise(nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, err := p.await(ctx); err != context.DeadlineExceeded {
		t.Fatalf("await error: %v", err)
	}
}

func TestEachBackendWithFake(t *testing.T) {
	t.Setenv("NYANJIT_E2E_BINFMT_READY", "")
	calls := []string{}
	foreign := "amd64"
	qemu := "qemu-x86_64"
	wineArch := "arm64"
	if runtime.GOARCH == "amd64" {
		foreign, qemu = "arm64", "qemu-aarch64"
		wineArch = "amd64"
	}
	b := &SystemBackend{Root: "/tmp"}
	b.commandFn = func(_ context.Context, _ string, env, argv []string) ExecResult {
		calls = append(calls, strings.Join(argv, " ")+" "+strings.Join(env, " "))
		return ExecResult{Status: "pass", Output: "ok"}
	}
	b.vmRunFn = func(_ context.Context, target Target, _ string, _, _ []string) (ExecResult, error) {
		calls = append(calls, "vm "+target.String())
		return ExecResult{Status: "pass", Output: "ok"}, nil
	}
	for _, target := range []Target{{OS: "linux", Arch: runtime.GOARCH}, {OS: "linux", Arch: foreign}, {OS: "windows", Arch: wineArch}, {OS: "freebsd", Arch: "arm64"}} {
		r, err := b.Run(context.Background(), target, "/tmp/fake", nil, nil)
		if err != nil || r.Status != "pass" {
			t.Fatalf("%s: %v %#v", target, err, r)
		}
	}
	if len(calls) != 4 {
		t.Fatalf("calls=%v", calls)
	}
	if !strings.Contains(calls[1], qemu) || !strings.Contains(calls[2], "wine") || calls[3] != "vm freebsd/arm64" {
		t.Fatalf("dispatch=%v", calls)
	}
	r, err := b.Run(context.Background(), Target{OS: "openbsd", Arch: "ppc64"}, "/tmp/fake", nil, nil)
	if err != nil || r.Status != "no runner" || len(calls) != 4 {
		t.Fatalf("no runner: %v %#v calls=%v", err, r, calls)
	}
}

func TestVMBinaryPathIsUniquePerVariant(t *testing.T) {
	a := Target{OS: "dragonfly", Arch: "amd64", GoVersion: "1.26.9"}
	b := Target{OS: "dragonfly", Arch: "amd64", GoVersion: "1.27.2"}
	if vmBinaryPath(a, "/cache/a/example") == vmBinaryPath(b, "/cache/b/example") {
		t.Fatal("Go versions would overwrite each other's VM binaries")
	}
}

func TestNetBSDBinaryPathAvoidsTmpfs(t *testing.T) {
	for _, arch := range []string{"386", "amd64", "arm", "arm64"} {
		got := vmBinaryPath(Target{OS: "netbsd", Arch: arch}, "/cache/example.test")
		if !strings.HasPrefix(got, "/root/") {
			t.Errorf("netbsd/%s path %q uses the small /tmp tmpfs", arch, got)
		}
	}
}

func TestSummaryLabelsSubstituteAndNoRunner(t *testing.T) {
	h := New(t.TempDir(), 1, Filter{})
	h.record(Result{Target: Target{OS: "solaris", Arch: "amd64", GoVersion: "1.27.2"}, Suite: "smoke", Name: "add", Status: "pass"})
	h.record(Result{Target: Target{OS: "openbsd", Arch: "arm", GoVersion: "1.27.2"}, Suite: "smoke", Name: "add", Status: "no runner", Reason: "EDK2 cannot read bsd.rd"})
	var summary strings.Builder
	h.PrintSummary(&summary)
	for _, want := range []string{"Solaris (illumos substitute, OmniOS r151058)", "NO RUNNER openbsd/arm: EDK2 cannot read bsd.rd"} {
		if !strings.Contains(summary.String(), want) {
			t.Errorf("summary %q lacks %q", summary.String(), want)
		}
	}
}

func TestQEMUCPUOverride(t *testing.T) {
	arch := "riscv64"
	if runtime.GOARCH == arch {
		arch = "amd64"
	}
	b := &SystemBackend{commandFn: func(_ context.Context, _ string, env []string, _ []string) ExecResult {
		count := 0
		for _, v := range env {
			if strings.HasPrefix(v, "QEMU_CPU=") {
				count++
				if v != "QEMU_CPU=max" {
					t.Errorf("override lost: %v", env)
				}
			}
		}
		if count != 1 {
			t.Errorf("CPU specified %d times: %v", count, env)
		}
		return ExecResult{}
	}}
	if _, err := b.Run(context.Background(), Target{OS: "linux", Arch: arch}, "/tmp/program", nil, []string{"QEMU_CPU=max"}); err != nil {
		t.Fatal(err)
	}
}
