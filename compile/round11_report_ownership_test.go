package compile_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/instrument/workload"
)

func TestVectorReportSnapshotsAreIndependent(t *testing.T) {
	u, err := lowerUnit(workload.Vectors(20), compile.TargetSpec{}, compile.Func{ABI: abi.C})
	if err != nil {
		t.Fatal(err)
	}
	first := u.Report()
	if len(first.VectorOperations()) == 0 {
		t.Fatal("missing vector report")
	}
	want := first.VectorOperations()[0]
	first.VectorOperations()[0].Position = -999
	template, err := u.Template()
	if err != nil {
		t.Fatal(err)
	}
	second := template.Report()
	if second.VectorOperations()[0] != want {
		t.Fatal("Unit report mutation reached template")
	}
	second.VectorOperations()[0].Position = -998
	if u.Report().VectorOperations()[0] != want || template.Report().VectorOperations()[0] != want {
		t.Fatal("report snapshots alias")
	}
}
