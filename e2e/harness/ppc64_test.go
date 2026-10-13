package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPPC64SysrootAndCPU(t *testing.T) {
	t.Setenv("NYANJIT_E2E_BINFMT_READY", "")
	for _, arch := range []string{"ppc64", "ppc64le"} {
		for _, cpu := range []string{"power8", "power9", "power10"} {
			target := Target{OS: "linux", Arch: arch, GOPPC64: cpu}
			if !target.Supported() {
				t.Fatalf("%s must claim native support", target)
			}
			b := &SystemBackend{commandFn: func(_ context.Context, _ string, env, argv []string) ExecResult {
				if !reflect.DeepEqual(argv, []string{qemuName(arch), "/binary", "-test.v", "-test.timeout=2m"}) {
					t.Errorf("argv=%v", argv)
				}
				got := strings.Join(env, "\n")
				for _, want := range []string{"QEMU_CPU=" + cpu, "QEMU_LD_PREFIX=/opt/nyanjit/sysroots/" + arch} {
					if !strings.Contains(got, want) {
						t.Errorf("env=%v; missing %s", env, want)
					}
				}
				return ExecResult{Status: "pass"}
			}}
			_, err := b.Run(context.Background(), target, "/binary", []string{"-test.v", "-test.timeout=2m"}, nil)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestPrivateBinfmtKeepsAllArguments(t *testing.T) {
	t.Setenv("NYANJIT_E2E_BINFMT_READY", "1")
	b := &SystemBackend{commandFn: func(_ context.Context, _ string, _, argv []string) ExecResult {
		if !reflect.DeepEqual(argv, []string{"/binary", "-test.v", "second"}) {
			t.Errorf("argv=%v", argv)
		}
		return ExecResult{Status: "pass"}
	}}
	_, err := b.Run(context.Background(), Target{OS: "linux", Arch: "ppc64"}, "/binary", []string{"-test.v", "second"}, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestBuildTestsBuildModeValidation(t *testing.T) {
	for _, mode := range []string{"", "pie", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			script := filepath.Join(root, "mode.star")
			src := fmt.Sprintf("def main():\n    build_tests(target(\"linux\", \"ppc64\"), [], buildmode=%q)\n", mode)
			if err := os.WriteFile(script, []byte(src), 0600); err != nil {
				t.Fatal(err)
			}
			err := New(root, 1, Filter{}).Run(context.Background(), script)
			if mode == "unknown" {
				if err == nil || !strings.Contains(err.Error(), "unsupported buildmode") {
					t.Fatalf("error=%v; want unsupported buildmode", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if mode != "" && buildModeSuffix(mode) == "" {
				t.Fatal("build mode must distinguish result names")
			}
		})
	}
}
