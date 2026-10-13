//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || loong64 || 386)

package varfuzz

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// TestLegacyCorpus replays exactly the seed × arithmetic-mode × config-index
// pairs formerly stored as eight copies of each byte stream in the fuzz corpus.
// Programs stay embedded once; mutation-driven fuzzing has its own small corpus.
func TestLegacyCorpus(t *testing.T) {
	entries, err := legacyCorpus.ReadDir("testdata/legacy")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int64]map[bool]bool{}
	attempted := 0
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), ".gz")
		parts := strings.Split(name, "-")
		if len(parts) != 3 || parts[0] != "seed" {
			t.Fatalf("invalid legacy name %q", name)
		}
		seed, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		fp, err := strconv.ParseBool(parts[2])
		if err != nil {
			t.Fatal(err)
		}
		if seen[seed] == nil {
			seen[seed] = map[bool]bool{}
		}
		seen[seed][fp] = true
		data, err := readLegacyProgram(name)
		if err != nil {
			t.Fatal(err)
		}
		for config := 0; config < 8; config++ {
			attempted++
			t.Run(fmt.Sprintf("%s/c%d", name, config), func(t *testing.T) {
				p := generateBytes(data, seed, fp, true)
				stats := checkProgram(t, p, paramsFor(seed), config, false)
				for key := range stats {
					if strings.HasPrefix(key, "discard:") {
						t.Fatalf("legacy case discarded: %s", key)
					}
				}
			})
		}
	}
	for _, seed := range []int64{239, 421, 1949, 445, 510, 602, 6, 102, 166, 217} {
		for _, fp := range []bool{false, true} {
			if !seen[seed][fp] {
				t.Errorf("missing legacy seed %d fp=%t", seed, fp)
			}
		}
	}
	if attempted != 256 {
		t.Fatalf("legacy seed/config pairs=%d, want 256", attempted)
	}
	t.Logf("legacy programs=%d config indices=8 pairs=%d (including all FP pairs)", len(entries), attempted)
}
