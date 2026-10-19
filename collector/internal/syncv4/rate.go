package syncv4

import (
	"math"
	"sort"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

func importRate(samples []float64, window time.Duration) *hubclient.V4ImportRate {
	if len(samples) < 6 || window <= 0 {
		return nil
	}
	valid := make([]float64, 0, len(samples))
	for _, sample := range samples {
		if sample >= 0 && !math.IsNaN(sample) && !math.IsInf(sample, 0) {
			valid = append(valid, sample)
		}
	}
	if len(valid) < 6 {
		return nil
	}
	sort.Float64s(valid)
	percentile := func(position float64) float64 {
		index := position * float64(len(valid)-1)
		lower := int(index)
		upper := min(lower+1, len(valid)-1)
		return valid[lower] + (valid[upper]-valid[lower])*(index-float64(lower))
	}
	return &hubclient.V4ImportRate{P25: percentile(0.25), P75: percentile(0.75), WindowSec: max(1, int64(window.Seconds()))}
}
