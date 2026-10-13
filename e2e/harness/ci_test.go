package harness

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"go.starlark.net/starlark"
)

func TestGoVersionFilter(t *testing.T) {
	h := New(t.TempDir(), 1, Filter{Target: "linux/ppc64le", GoVersion: "1.27.2"})
	for _, tt := range []struct {
		target Target
		want   bool
	}{
		{Target{OS: "linux", Arch: "ppc64le", GoVersion: "1.27.2", GOPPC64: "power8"}, true},
		{Target{OS: "linux", Arch: "ppc64le", GoVersion: "1.27.2", GOPPC64: "power10"}, true},
		{Target{OS: "linux", Arch: "ppc64le", GoVersion: "1.26.9"}, false},
		{Target{OS: "linux", Arch: "ppc64", GoVersion: "1.27.2"}, false},
	} {
		if got := h.selected(tt.target, "matrix"); got != tt.want {
			t.Errorf("%s: selected=%v", tt.target.Key(), got)
		}
	}
}

func TestSweepCount(t *testing.T) {
	for _, value := range []string{"unset", "500", "2000", "0", "-1", "oops", ""} {
		t.Run(value, func(t *testing.T) {
			if value == "unset" {
				// Restore the caller's environment through t.Setenv cleanup.
				t.Setenv("NYANJIT_E2E_VAR_SWEEP", "")
				if err := os.Unsetenv("NYANJIT_E2E_VAR_SWEEP"); err != nil {
					t.Fatal(err)
				}
			} else {
				t.Setenv("NYANJIT_E2E_VAR_SWEEP", value)
			}
			root := filepath.Clean("../..")
			h := New(root, 1, Filter{Suite: "nothing"})
			h.script = filepath.Join(root, "e2e", "matrix.star")
			h.loaded = make(map[string]starlark.StringDict)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			globals, err := starlark.ExecFile(h.thread("test", ctx), h.script, nil, h.predeclared())
			if err != nil {
				t.Fatal(err)
			}
			result, err := h.invoke(ctx, globals["sweep_count"], nil)
			if value == "unset" || value == "500" || value == "2000" {
				want := value
				if value == "unset" {
					want = "2000"
				}
				got, _ := starlark.AsString(result)
				if err != nil || got != want {
					t.Fatalf("got=%q err=%v want=%s", got, err, want)
				}
			} else if err == nil {
				t.Fatal("invalid count accepted")
			}
		})
	}
}

func TestGoCommandFallbackVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell probe")
	}
	root := t.TempDir()
	// This version cannot exist under /opt; the fake executable verifies that
	// fallback dispatch carries an explicit toolchain instead of using PATH's
	// version while reporting a different version.
	if err := os.WriteFile(filepath.Join(root, "go"), []byte("#!/bin/sh\nprintf '%s\\n' \"$GOTOOLCHAIN\" \"$GOOS/$GOARCH\" \"$*\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GOTOOLCHAIN", "local")
	h := New(root, 1, Filter{})
	got := h.goCommand(context.Background(), Target{OS: "linux", Arch: "386", GoVersion: "9.99.0"}, "version")
	if got.ExitCode != 0 || !strings.Contains(got.Output, "go9.99.0\nlinux/386\nversion\n") {
		t.Fatalf("result=%+v", got)
	}
}

func TestRunScriptSettings(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell entrypoint")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, reuse := range []bool{false, true} {
		name := "build"
		if reuse {
			name = "reuse"
		}
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			log := filepath.Join(directory, "docker.log")
			fake := "#!/bin/sh\nprintf 'CALL\\n' >> \"$NYANJIT_TEST_DOCKER_LOG\"\nprintf '%s\\n' \"$@\" >> \"$NYANJIT_TEST_DOCKER_LOG\"\n"
			if err := os.WriteFile(filepath.Join(directory, "docker"), []byte(fake), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("NYANJIT_TEST_DOCKER_LOG", log)
			t.Setenv("NYANJIT_E2E_VM_OSES", "")
			t.Setenv("NYANJIT_E2E_OPENBSD_ARCHS", "arm64")
			t.Setenv("NYANJIT_E2E_VAR_SWEEP", "500")
			t.Setenv("NYANJIT_E2E_LOONG64_QEMU", "debian")
			t.Setenv("NYANJIT_E2E_CACHE", directory)
			t.Setenv("NYANJIT_E2E_IMAGE", "")
			if reuse {
				t.Setenv("NYANJIT_E2E_IMAGE", "nyanjit:prebuilt")
			}
			command := exec.Command(filepath.Join(root, "e2e", "run.sh"), "e2e/matrix.star", "-target", "linux/riscv64", "-suite", "matrix")
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("run.sh: %v\n%s", err, output)
			}
			data, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			got := string(data)
			if reuse {
				if strings.Contains(got, "CALL\nbuild\n") || !strings.Contains(got, "nyanjit:prebuilt\n") {
					t.Fatalf("expected image reuse:\n%s", got)
				}
			} else if !strings.Contains(got, "VM_OSES=\n") || !strings.Contains(got, "OPENBSD_ARCHS=arm64\n") {
				t.Fatalf("build args altered:\n%s", got)
			}
			if !strings.Contains(got, "NYANJIT_E2E_LOONG64_QEMU=debian\n") || !strings.Contains(got, "NYANJIT_E2E_VAR_SWEEP=500\n") || !strings.Contains(got, "-target\nlinux/riscv64\n-suite\nmatrix\ne2e/matrix.star\n") {
				t.Fatalf("run settings:\n%s", got)
			}
			_, kvmErr := os.Stat("/dev/kvm")
			if strings.Contains(got, "--device\n/dev/kvm\n") != (kvmErr == nil) {
				t.Fatalf("incorrect optional KVM:\n%s", got)
			}
		})
	}
}

func TestPackageListIgnoresToolchainDownloadNotice(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell probe")
	}
	root := t.TempDir()
	script := "#!/bin/sh\nprintf 'go: downloading go9.99.0 (linux/arm64)\\n' >&2\nif [ \"$2\" = fail ]; then exit 1; fi\nprintf 'example.org/one\\nexample.org/two\\n'\n"
	if err := os.WriteFile(filepath.Join(root, "go"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	h := New(root, 1, Filter{})
	target := Target{OS: "linux", Arch: "arm64", GoVersion: "9.99.0"}
	got := h.goCommand(context.Background(), target, "list", "./...")
	if got.ExitCode != 0 || got.Output != "example.org/one\nexample.org/two\n" {
		t.Fatalf("package listing=%+v", got)
	}
	got = h.goCommand(context.Background(), target, "list", "fail")
	if got.ExitCode == 0 || !strings.Contains(got.Output, "go: downloading") {
		t.Fatalf("failure diagnostic lost: %+v", got)
	}
}
