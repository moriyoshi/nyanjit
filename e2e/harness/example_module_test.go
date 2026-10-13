package harness

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"go.starlark.net/starlark"
)

func TestNestedModuleDiscoveryAndBuild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell probe")
	}
	root := t.TempDir()
	for _, dir := range []string{"backend", "examples/snake_termbox"} {
		module := filepath.Join(root, dir)
		if err := os.MkdirAll(module, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(module, "go.mod"), []byte("module github.com/moriyoshi/nyanjit/"+dir+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	script := `#!/bin/sh
printf '%s\n' "$*" >> calls
if [ "$1" = '-C' ]; then
 dir=$2
 shift 2
 case "$1" in
 list) if [ "$dir" = backend ]; then echo github.com/moriyoshi/nyanjit/backend/machine; else echo github.com/moriyoshi/nyanjit/examples/snake_termbox; fi;;
 test) exit 0;;
 esac
else
 case "$1" in
 list) echo github.com/moriyoshi/nyanjit/compile;;
 test) exit 0;;
 esac
fi
`
	if err := os.WriteFile(filepath.Join(root, "go"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	h := New(root, 1, Filter{})
	src := `def main():
 t = target("linux", "386", go_version="9.99.0")
 p = packages(t)
 if len(p) != 3:
  fail("nested module missing: " + str(p))
 build_tests(t, p)
main()
`
	if _, err := starlark.ExecFile(h.thread("test", context.Background()), "test.star", src, h.predeclared()); err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(filepath.Join(root, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(calls)), "\n")
	if len(lines) != 6 || !strings.HasPrefix(lines[1], "-C backend list ") || !strings.HasPrefix(lines[2], "-C examples/snake_termbox list ") ||
		!strings.HasPrefix(lines[3], "test -c ") || !strings.HasSuffix(lines[3], " github.com/moriyoshi/nyanjit/compile") ||
		!strings.HasPrefix(lines[4], "-C backend test -c ") || !strings.HasSuffix(lines[4], " github.com/moriyoshi/nyanjit/backend/machine") ||
		!strings.HasPrefix(lines[5], "-C examples/snake_termbox test -c ") || !strings.HasSuffix(lines[5], " github.com/moriyoshi/nyanjit/examples/snake_termbox") {
		t.Fatalf("incorrect module routing:\n%s", calls)
	}
}
