package instrument_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/moriyoshi/nyanjit"

// moduleRoot returns the main module's source directory. go test runs this
// binary in its package directory; the E2E matrix runs it from elsewhere,
// without the source tree these checks inspect.
func moduleRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil || !strings.HasPrefix(string(data), "module "+module+"\n") {
		t.Skip("the module source tree is not available")
	}
	return root
}

// Production packages must not import instrumentation. Tests, examples and
// instrument packages themselves may.
func TestOnlyTestsAndExamplesImportInstrument(t *testing.T) {
	root := moduleRoot(t)
	checked := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			name := d.Name()
			if rel != "." && (strings.HasPrefix(name, ".") || name == "testdata" || name == "examples" || name == "instrument" || name == "e2e") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		checked++
		for _, spec := range file.Imports {
			imported, _ := strconv.Unquote(spec.Path.Value)
			if imported == module+"/instrument" || strings.HasPrefix(imported, module+"/instrument/") {
				t.Errorf("%s imports %s", rel, imported)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 100 {
		t.Fatalf("checked only %d files; the walk missed the module", checked)
	}
}

// TestBackendModuleStandsAlone checks that the backend module neither
// requires the main module nor imports a main-module package, tests included.
// It runs go list without the workspace and for every Linux target, so files
// behind build constraints are checked too.
func TestBackendModuleStandsAlone(t *testing.T) {
	checkStandsAlone(t, "backend", []string{module + "/backend"}, 10)
}

// TestStackVMUsesOnlyBackend checks that examples/stackvm, the front end
// built from the backend toolkit, needs only the backend module: no ir,
// builder, compile or other main-module package.
func TestStackVMUsesOnlyBackend(t *testing.T) {
	checkStandsAlone(t, "examples/stackvm", []string{module + "/backend", module + "/examples/stackvm"}, 6)
}

func checkStandsAlone(t *testing.T, dir string, allowed []string, minimum int) {
	gotool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("the go command is not available")
	}
	root := filepath.Join(moduleRoot(t), dir)
	run := func(env []string, args ...string) []string {
		t.Helper()
		cmd := exec.Command(gotool, args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), append([]string{"GOWORK=off", "GOFLAGS="}, env...)...)
		out, err := cmd.Output()
		if err != nil {
			var stderr []byte
			if exit, ok := err.(*exec.ExitError); ok {
				stderr = exit.Stderr
			}
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, stderr)
		}
		return strings.Fields(string(out))
	}
	for _, path := range run(nil, "list", "-m", "-f", "{{.Path}}", "all") {
		if path == module {
			t.Errorf("%s/go.mod requires %s", dir, module)
		}
	}
	permitted := func(path string) bool {
		// go list -test names a test main package "<path>.test".
		path = strings.TrimSuffix(path, ".test")
		for _, prefix := range allowed {
			if path == prefix || strings.HasPrefix(path, prefix+"/") {
				return true
			}
		}
		return false
	}
	for _, arch := range []string{"amd64", "arm64", "386", "riscv64", "ppc64le", "loong64"} {
		seen := 0
		for _, path := range run([]string{"GOOS=linux", "GOARCH=" + arch, "CGO_ENABLED=0"}, "list", "-deps", "-test", "-f", "{{.ImportPath}}", "./...") {
			if path != module && !strings.HasPrefix(path, module+"/") {
				continue
			}
			seen++
			if !permitted(path) {
				t.Errorf("linux/%s: %s depends on %s", arch, dir, path)
			}
		}
		if seen < minimum {
			t.Fatalf("linux/%s: go list found only %d packages of %s", arch, seen, strings.Join(allowed, ", "))
		}
	}
}
