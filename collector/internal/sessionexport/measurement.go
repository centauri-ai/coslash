package sessionexport

import (
	"errors"
	"fmt"
	"sort"

	"github.com/centauri-ai/coslash/collector/internal/session"
	snapshotv1 "github.com/centauri-ai/coslash/collector/snapshot/v1"
)

// MeasurementReport contains sizes only. It intentionally cannot carry source
// IDs, paths, text, or any other corpus content.
type MeasurementReport struct {
	SchemaVersion       string  `json:"schemaVersion"`
	CollectorVersion    string  `json:"collectorVersion"`
	CorpusSize          int     `json:"corpusSize"`
	P50Bytes            int     `json:"p50Bytes"`
	P95Bytes            int     `json:"p95Bytes"`
	P99Bytes            int     `json:"p99Bytes"`
	MaximumBytes        int     `json:"maximumBytes"`
	FittedMaximumBytes  int     `json:"fittedMaximumBytes"`
	AggregateLimitBytes int     `json:"aggregateLimitBytes"`
	Degraded            int     `json:"degraded"`
	DegradationRate     float64 `json:"degradationRate"`
	Rejected            int     `json:"rejected"`
	RejectionRate       float64 `json:"rejectionRate"`
}

// MeasureCorpus maps the approved sanitized corpus and reports canonical sizes
// without retaining or returning serialized content.
func MeasureCorpus(corpus []session.Session, options func(int) BuildOptions) (MeasurementReport, error) {
	report := MeasurementReport{
		SchemaVersion: snapshotv1.SchemaVersion, CorpusSize: len(corpus), AggregateLimitBytes: snapshotv1.MaxPayloadBytes,
	}
	if len(corpus) == 0 {
		return report, fmt.Errorf("measurement corpus is empty")
	}
	sizes := make([]int, 0, len(corpus))
	for i, local := range corpus {
		buildOptions := options(i)
		if i == 0 {
			report.CollectorVersion = buildOptions.CollectorVersion
		}
		if buildOptions.CollectorVersion != report.CollectorVersion {
			return MeasurementReport{}, fmt.Errorf("corpus mixes collector versions")
		}
		snapshot, err := Build(local, buildOptions)
		if err != nil {
			return MeasurementReport{}, fmt.Errorf("measure corpus item %d: %w", i, err)
		}
		size, err := snapshotv1.Size(snapshot)
		if err != nil {
			return MeasurementReport{}, fmt.Errorf("measure corpus item %d: %w", i, err)
		}
		sizes = append(sizes, size)
		fittedSize := size
		if size > snapshotv1.MaxPayloadBytes {
			fitted, fitErr := fitAggregate(snapshot)
			if fitErr != nil {
				if errors.Is(fitErr, snapshotv1.ErrOversized) {
					report.Rejected++
					continue
				}
				return MeasurementReport{}, fmt.Errorf("fit corpus item %d: %w", i, fitErr)
			}
			report.Degraded++
			fittedSize, err = snapshotv1.Size(fitted)
			if err != nil {
				return MeasurementReport{}, fmt.Errorf("measure fitted corpus item %d: %w", i, err)
			}
		}
		if fittedSize > report.FittedMaximumBytes {
			report.FittedMaximumBytes = fittedSize
		}
	}
	sort.Ints(sizes)
	report.P50Bytes = nearestRank(sizes, 50)
	report.P95Bytes = nearestRank(sizes, 95)
	report.P99Bytes = nearestRank(sizes, 99)
	report.MaximumBytes = sizes[len(sizes)-1]
	report.DegradationRate = float64(report.Degraded) / float64(len(sizes))
	report.RejectionRate = float64(report.Rejected) / float64(len(sizes))
	return report, nil
}

func nearestRank(sorted []int, percentile int) int {
	index := (percentile*len(sorted) + 99) / 100
	return sorted[max(index-1, 0)]
}
