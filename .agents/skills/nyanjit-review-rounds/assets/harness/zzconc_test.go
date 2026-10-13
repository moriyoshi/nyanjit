//go:build linux && arm64

package varfuzz

// Concurrency determinism check (reviewer-owned): serial listings vs listings
// produced from 16 goroutines in random order, several rounds.
// ZZCONC=1 ZZCONC_N=2000 ZZCONC_ROUNDS=5 ZZCONC_JOBS=4 ZZCONC_SEED=920000000

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

type zzJob struct {
	prog   ir.Input
	desc   string
	opt    zzOpt
	native bool
}

func zzRun(j *zzJob) (s string) {
	defer func() {
		if r := recover(); r != nil {
			s = fmt.Sprintf("PANIC %v", r)
			_ = debug.Stack
		}
	}()
	if j.native {
		code, rep, err := zzEmit(j.prog, j.opt)
		if err != nil {
			return "ERR " + err.Error()
		}
		d := sha256.Sum256(code)
		return fmt.Sprintf("OK %s %d %d %d %v %v", hex.EncodeToString(d[:]), rep.CodeBytes, rep.StackBytes, rep.WorkspaceBytes, rep.FlagsClobbered, rep.CPU.Extensions)
	}
	l, err := zzListing(j.prog, j.opt)
	if err != nil {
		return "ERR " + err.Error()
	}
	d := sha256.Sum256([]byte(l))
	return "OK " + hex.EncodeToString(d[:]) + fmt.Sprint(" ", len(l))
}

func TestZZConc(t *testing.T) {
	if os.Getenv("ZZCONC") == "" {
		t.Skip()
	}
	N := int(zzEnv("ZZCONC_N", 2000))
	rounds := int(zzEnv("ZZCONC_ROUNDS", 5))
	per := int(zzEnv("ZZCONC_JOBS", 4))
	base := zzEnv("ZZCONC_SEED", 920000000)
	gens := []string{"rand", "varfuzz", "scope", "work", "pscope"}
	r := rand.New(rand.NewSource(base))
	var jobs []*zzJob
	for i := 0; i < N; i++ {
		gen := gens[i%len(gens)]
		seed := base + int64(i)
		b, err := zzBuild(gen, seed)
		if err != nil {
			continue
		}
		prog, err := b.Finish()
		if err != nil {
			continue
		}
		for k := 0; k < per; k++ {
			arch := zzArches[r.Intn(len(zzArches))]
			cs := zzConfigs(arch, true)
			c := cs[r.Intn(len(cs))]
			jobs = append(jobs, &zzJob{prog, fmt.Sprintf("%s %d %s %s", gen, seed, arch, c.name), c.opt, false})
		}
		cs := zzConfigs("arm64", false)
		c := cs[r.Intn(len(cs))]
		jobs = append(jobs, &zzJob{prog, fmt.Sprintf("%s %d arm64 native-%s", gen, seed, c.name), c.opt, true})
	}
	serial := make([]string, len(jobs))
	okc := 0
	for i, j := range jobs {
		serial[i] = zzRun(j)
		if serial[i][:2] == "OK" {
			okc++
		}
	}
	t.Logf("GOMAXPROCS=%d jobs=%d ok=%d err=%d", runtime.GOMAXPROCS(0), len(jobs), okc, len(jobs)-okc)
	var mism atomic.Int64
	for round := 0; round < rounds; round++ {
		order := r.Perm(len(jobs))
		next := atomic.Int64{}
		var wg sync.WaitGroup
		for w := 0; w < 16; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					k := int(next.Add(1) - 1)
					if k >= len(order) {
						return
					}
					i := order[k]
					got := zzRun(jobs[i])
					if got != serial[i] {
						if mism.Add(1) <= 20 {
							t.Errorf("round %d %s: serial=%q concurrent=%q", round, jobs[i].desc, zzFirst(serial[i]), zzFirst(got))
						}
					}
				}
			}()
		}
		wg.Wait()
		if round%2 == 1 {
			runtime.GC() // let sync.Pool drop/keep entries
		}
	}
	t.Logf("rounds=%d comparisons=%d mismatches=%d", rounds, rounds*len(jobs), mism.Load())
}
