package cpuprofile

import "testing"

func TestLoong64HWCAP(t *testing.T) {
	for _, v := range []uint64{0, 1 << 4, 1 << 5, 3 << 4} {
		p := loong64HWCAP(v)
		if p.Has("lsx") != (v&16 != 0) || p.Has("lasx") != (v&32 != 0) {
			t.Fatalf("hwcap%#x: %+v", v, p)
		}
	}
}
