package compile

import "testing"

// withoutVarPromotion lowers Vars through their homes for the rest of t.
func withoutVarPromotion(t testing.TB) {
	old := variablePromotion
	variablePromotion = false
	t.Cleanup(func() { variablePromotion = old })
}

// withVarPromotion promotes Vars for the rest of t, even when
// NYANJIT_INTERNAL_VAR_PROMOTION=0 disables promotion for the run.
func withVarPromotion(t testing.TB) {
	old := variablePromotion
	variablePromotion = true
	t.Cleanup(func() { variablePromotion = old })
}
