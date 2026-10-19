package engine

import (
	"math"
	"testing"
)

func TestRetryProgressIsMonotonic(t *testing.T) {
	var got []float64
	rp := newRetryProgress(func(_ string, pct float64) { got = append(got, pct) })
	rp.within("compressing", 0.0)
	rp.within("compressing", 0.5)
	rp.within("compressing", 1.0)
	rp.fail()
	rp.within("compressing", 0.0)
	rp.within("compressing", 1.0)
	rp.succeed()
	rp.finish("saving")
	for i := 1; i < len(got); i++ {
		if got[i]+0.001 < got[i-1] {
			t.Fatalf("progress went backwards: %v", got)
		}
	}
	if got[len(got)-1] != 100 {
		t.Fatalf("final = %v", got[len(got)-1])
	}
	if math.Abs(got[0]-0) > 0.001 {
		t.Fatalf("first = %v", got[0])
	}
}
