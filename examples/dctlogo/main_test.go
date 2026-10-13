package main

import (
	"bytes"
	"errors"
	"io"
	"math"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func requireNativeFP(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("native floating point execution requires Linux")
	}
	switch runtime.GOARCH {
	case "amd64", "arm64", "riscv64", "ppc64le", "386":
	default:
		t.Skip("architecture has no native floating point backend")
	}
}

var monochromeLogo = []string{
	"                           ",
	"     ##             ##     ",
	"    #  #           #  #    ",
	"    # # ########### # #    ",
	"    #                 #    ",
	"   #    ##       ##    #   ",
	"  #     ##  ###  ##     #  ",
	"#####        #        #####",
	"  #       #######       #  ",
	" ####      # # #      #### ",
	"  #        #   #        #  ",
	"   ##       ###       ##   ",
	"     ####         ####     ",
	"         #########         ",
	"                           ",
	"                           ",
	"#  # # #  #  #  #  ## # ###",
	"## # # # # # ## #   # #  # ",
	"## # # # # # ## #   # #  # ",
	"# ##  #  ### # ##   # #  # ",
	"# ##  #  # # # ## # # #  # ",
	"#  #  #  # # #  #  #  #  # ",
}

func TestEmbeddedCoefficientsReconstructLogo(t *testing.T) {
	requireNativeFP(t)
	samples, err := inverseDCT(logoCoefficients[:])
	if err != nil {
		t.Fatal(err)
	}
	for y, row := range monochromeLogo {
		for x, pixel := range row {
			want := 0.0
			if pixel == '#' {
				want = 1
			}
			if got := samples[y*width+x]; math.Abs(got-want) > 1e-9 {
				t.Fatalf("pixel (%d, %d) = %.12g, want %g", x, y, got, want)
			}
		}
	}
}

func TestJITInverseMatchesDirectDCT(t *testing.T) {
	requireNativeFP(t)
	for _, terms := range []int{1, 2, 64, sampleCount} {
		t.Run(strconv.Itoa(terms), func(t *testing.T) {
			coefficients := make([]float64, terms)
			for k := range coefficients {
				coefficients[k] = math.Sin(float64(k) + 0.3)
			}
			got, err := inverseDCT(coefficients)
			if err != nil {
				t.Fatal(err)
			}
			for n, sample := range got {
				var want float64
				for k, coefficient := range coefficients {
					want += coefficient * dctScale(k, sampleCount) * math.Cos(math.Pi*(float64(n)+0.5)*float64(k)/sampleCount)
				}
				if math.Abs(sample-want) > 1e-9 {
					t.Fatalf("sample %d = %.12g, want %.12g", n, sample, want)
				}
			}
		})
	}
}

func TestRunReconstructsMonochromeLogo(t *testing.T) {
	requireNativeFP(t)
	want := strings.NewReplacer("#", "██", " ", "  ").Replace(strings.Join(monochromeLogo, "\n") + "\n")
	var got bytes.Buffer
	if err := run(&got); err != nil {
		t.Fatal(err)
	}
	if got.String() != want {
		t.Fatal("reconstructed terminal logo differs from the expected logo")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestRenderPropagatesOutputError(t *testing.T) {
	if err := render(failingWriter{}, make([]float64, sampleCount)); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("got %v, want writer error", err)
	}
}

// The direct reference evaluates each cosine independently of the generated
// recurrence, using Go math only in tests.
func dctScale(k, n int) float64 {
	if k == 0 {
		return 1 / math.Sqrt(float64(n))
	}
	return math.Sqrt(2 / float64(n))
}
