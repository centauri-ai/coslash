package syncv4

import (
	"math"
	"testing"
	"time"
)

func TestImportRateNeedsSixMeasuredSamples(t *testing.T) {
	if got := importRate([]float64{10, 20, 30, 40, 50}, 2*time.Minute); got != nil {
		t.Fatalf("five samples gave a rate: %+v", got)
	}
	if got := importRate([]float64{10, 20, math.NaN(), 30, 40, 50, math.Inf(1)}, 2*time.Minute); got != nil {
		t.Fatalf("invalid samples counted toward the minimum: %+v", got)
	}
	if got := importRate([]float64{10, 20, 30, 40, 50, 60}, 0); got != nil {
		t.Fatalf("missing window gave a rate: %+v", got)
	}
}

func TestImportRateUsesMeasuredQuantiles(t *testing.T) {
	got := importRate([]float64{60, 10, 50, 20, 40, 30}, 2*time.Minute)
	if got == nil || got.P25 != 22.5 || got.P75 != 47.5 || got.WindowSec != 120 {
		t.Fatalf("rate = %+v", got)
	}
}
