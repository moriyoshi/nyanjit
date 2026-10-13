package harness

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"go.starlark.net/starlark"
)

func TestLoong64RunnerContract(t *testing.T) {
	t.Setenv("NYANJIT_E2E_LOONG64_QEMU", "tonistiigi")
	target := Target{OS: "linux", Arch: "loong64"}
	if !target.Supported() || qemuName(target.Arch) != "qemu-loongarch64" || qemuCPU(target) != "la464,lsx=off,lasx=off" {
		t.Fatal("loong64 execution contract missing")
	}
	t.Setenv("NYANJIT_E2E_BINFMT_READY", "")
	for _, override := range []bool{false, true} {
		b := &SystemBackend{commandFn: func(_ context.Context, _ string, env, argv []string) ExecResult {
			wantArgs := []string{qemuName(target.Arch), "/binary", "/binary", "-test.run=^TestCABIStackAlignment$", "-test.timeout=2m"}
			if !slices.Equal(argv, wantArgs) {
				t.Errorf("argv=%q want%q", argv, wantArgs)
			}
			want := "QEMU_CPU=la464,lsx=off,lasx=off"
			if override {
				want = "QEMU_CPU=max"
			}
			if !slices.Contains(env, "QEMU_LD_PREFIX=/opt/nyanjit/sysroots/loong64") {
				t.Errorf("missing isolated sysroot in %v", env)
			}
			if !slices.Contains(env, want) {
				t.Errorf("missing %s in%v", want, env)
			}
			return ExecResult{Status: "pass"}
		}}
		var env []string
		if override {
			env = []string{"QEMU_CPU=max"}
		}
		_, err := b.Run(context.Background(), target, "/binary", []string{"-test.run=^TestCABIStackAlignment$", "-test.timeout=2m"}, env)
		if err != nil {
			t.Fatal(err)
		}
	}
}

// The image uses tonistiigi's preserve-argv0 QEMU patch. Without the extra
// argv[0], an apparently passing filtered probe might run a different test set.
func TestExplicitQEMUInvocationPreservesTestFlags(t *testing.T) {
	t.Setenv("NYANJIT_E2E_LOONG64_QEMU", "tonistiigi")
	t.Setenv("NYANJIT_E2E_BINFMT_READY", "")
	for _, arch := range []string{"loong64", "amd64", "386", "riscv64"} {
		target := Target{OS: "linux", Arch: arch}
		if target.Runner() != "qemu-user" {
			continue
		}
		t.Run(arch, func(t *testing.T) {
			args := []string{"-test.run=^OnlyThis$", "-test.count=2", "-test.v", "-test.timeout=2m"}
			b := &SystemBackend{commandFn: func(_ context.Context, _ string, _, argv []string) ExecResult {
				want := append([]string{"/binary.test", "/binary.test"}, args...)
				if len(argv) != len(want)+1 || filepath.Base(argv[0]) != qemuName(arch) || !slices.Equal(argv[1:], want) {
					t.Fatalf("argv=%q", argv)
				}
				return ExecResult{Status: "pass"}
			}}
			if _, err := b.Run(context.Background(), target, "/binary.test", args, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// The optional stress suite must preserve its GC settings and repeat count,
// and must retain a runtime panic as a failure rather than retrying it.
func TestLoong64GCStressProfile(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "pass", true: "failure"}[failure], func(t *testing.T) {
			root := filepath.Clean("../..")
			h := New(root, 1, Filter{})
			h.script = filepath.Join(root, "e2e", "loong64_gc.star")
			h.loaded = make(map[string]starlark.StringDict)
			calls := 0
			h.Backend = &SystemBackend{commandFn: func(ctx context.Context, _ string, env, argv []string) ExecResult {
				calls++
				for _, want := range []string{"QEMU_CPU=max", "GOGC=10", "GODEBUG=gccheckmark=1"} {
					if !slices.Contains(env, want) {
						t.Errorf("missing %s in %q", want, env)
					}
				}
				for _, want := range []string{"-test.v", "-test.count=3", "-test.timeout=20m"} {
					if !slices.Contains(argv, want) {
						t.Errorf("missing %s in %q", want, argv)
					}
				}
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 1260*time.Second || time.Until(deadline) < 1250*time.Second {
					t.Errorf("missing bounded run deadline")
				}
				if failure {
					return ExecResult{Status: "fail", ExitCode: 2, Output: "fatal error: checkmark found unmarked object"}
				}
				return ExecResult{Status: "pass", Output: "PASS"}
			}}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			globals, err := starlark.ExecFile(h.thread("test", ctx), h.script, nil, h.predeclared())
			if err != nil {
				t.Fatal(err)
			}
			_, err = h.invoke(ctx, globals["run_gc_profile"], starlark.Tuple{
				targetValue(Target{OS: "linux", Arch: "loong64", GoVersion: "1.27.2"}),
				starlark.String("/varfuzz.test"), starlark.String("max"), starlark.String("loong64-gc-max"),
			})
			if err != nil {
				t.Fatal(err)
			}
			results := h.Results()
			want := "pass"
			if failure {
				want = "fail"
			}
			if calls != 1 || len(results) != 1 || results[0].Status != want {
				t.Fatalf("calls=%d results=%+v", calls, results)
			}
			if failure && !strings.Contains(results[0].Output, "checkmark found unmarked object") {
				t.Fatal("GC diagnostic lost")
			}
		})
	}
}

func TestLoong64DebianRunnerArgv(t *testing.T) {
	t.Setenv("NYANJIT_E2E_LOONG64_QEMU", "debian")
	for _, ready := range []string{"", "1"} {
		t.Run("binfmt="+ready, func(t *testing.T) {
			t.Setenv("NYANJIT_E2E_BINFMT_READY", ready)
			b := &SystemBackend{commandFn: func(_ context.Context, _ string, env, argv []string) ExecResult {
				want := []string{"/binary.test", "-test.run=^Selected$", "-test.count=2"}
				if ready == "" {
					want = append([]string{"/opt/nyanjit/qemu-debian/qemu-loongarch64"}, want...)
				}
				if !slices.Equal(argv, want) {
					t.Errorf("argv=%q want=%q", argv, want)
				}
				for _, setting := range []string{"QEMU_CPU=max", "QEMU_LD_PREFIX=/opt/nyanjit/sysroots/loong64"} {
					if !slices.Contains(env, setting) {
						t.Errorf("missing %q in %q", setting, env)
					}
				}
				return ExecResult{Status: "pass"}
			}}
			_, err := b.Run(context.Background(), Target{OS: "linux", Arch: "loong64"}, "/binary.test", []string{"-test.run=^Selected$", "-test.count=2"}, []string{"QEMU_CPU=max"})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLoong64RunnerRejectsUnknownSelection(t *testing.T) {
	t.Setenv("NYANJIT_E2E_LOONG64_QEMU", "typo")
	b := &SystemBackend{commandFn: func(_ context.Context, _ string, _, _ []string) ExecResult {
		t.Fatal("invalid selection executed")
		return ExecResult{}
	}}
	_, err := b.Run(context.Background(), Target{OS: "linux", Arch: "loong64"}, "/binary", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "NYANJIT_E2E_LOONG64_QEMU") {
		t.Fatalf("err=%v", err)
	}
}
