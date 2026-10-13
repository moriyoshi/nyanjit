//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || loong64 || 386)

package varfuzz

import (
	"os"
	"os/exec"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/goregister"
	"github.com/moriyoshi/nyanjit/compile"
)

// Go registrations last for the process, so a long fuzzing process uses them
// all. The fuzz check then skips Go-managed configurations rather than
// reporting the program (security audit 2026-10-25, L24). The budget is used
// up in a child process, which no other test shares.
func TestScopedVarsSurviveRegistrationBudget(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("runtime registration disabled")
	}
	if os.Getenv("NYANJIT_VAR_BUDGET_CHILD") != "1" {
		if testing.Short() {
			t.Skip("registers thousands of functions")
		}
		cmd := exec.Command(os.Args[0], "-test.run=^TestScopedVarsSurviveRegistrationBudget$", "-test.v", "-test.timeout=10m")
		cmd.Env = append(os.Environ(), "NYANJIT_VAR_BUDGET_CHILD=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("child: %v\n%s", err, output)
		}
		return
	}
	var p *Program
	var params [3]uint64
	for index := uint64(0); p == nil; index++ {
		candidate := generateBytes(sweepBytes(index), int64(index), false, false)
		params = paramsFor(int64(index))
		if _, bad := Interpret(candidate, params); bad == "" {
			p = candidate
		}
	}
	var managed Config
	for _, c := range configs(p) {
		if c.opt.Context == compile.ContextGoManaged {
			managed = c
		}
	}
	b := n.NewBuilder()
	b.Return(b.Param())
	identity, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if used, limit := goregister.RegisteredFunctions(); used >= limit {
			break
		}
		if _, err := n.Compile(identity, compile.TargetSpec{}, managed.opt); err != nil {
			t.Fatalf("registration %d: %v", func() int { used, _ := goregister.RegisteredFunctions(); return used }(), err)
		}
	}
	stats := checkProgram(t, p, params, 0, true)
	if stats["limit:"+managed.name] == 0 {
		t.Fatalf("%s was not skipped: %v", managed.name, stats)
	}
}
