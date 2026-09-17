package fullsessionv1

import (
	"fmt"
	"sort"
)

// MeasurementReport contains counts and sizes only. It intentionally cannot
// carry source/session identities, paths, or parsed text.
type MeasurementReport struct {
	SchemaVersion      string `json:"schemaVersion"`
	CollectorVersion   string `json:"collectorVersion"`
	CorpusSize         int    `json:"corpusSize"`
	P50Bytes           int    `json:"p50Bytes"`
	P95Bytes           int    `json:"p95Bytes"`
	P99Bytes           int    `json:"p99Bytes"`
	MaximumBytes       int    `json:"maximumBytes"`
	TotalChangeBodies  int    `json:"totalChangeBodies"`
	MaximumChangeBytes int    `json:"maximumChangeBytes"`
}

func Measure(records []Record, collectorVersion string) (MeasurementReport, error) {
	report := MeasurementReport{SchemaVersion: SchemaVersion, CollectorVersion: collectorVersion, CorpusSize: len(records)}
	if collectorVersion == "" {
		return MeasurementReport{}, fmt.Errorf("collector version is required")
	}
	if len(records) == 0 {
		return report, fmt.Errorf("measurement corpus is empty")
	}
	sizes := make([]int, 0, len(records))
	for index, record := range records {
		data, err := Marshal(record)
		if err != nil {
			return MeasurementReport{}, fmt.Errorf("measure record %d: %w", index, err)
		}
		sizes = append(sizes, len(data))
		for _, edit := range record.Session.FileEdits {
			for _, change := range edit.Changes {
				report.TotalChangeBodies++
				report.MaximumChangeBytes = max(report.MaximumChangeBytes, change.ByteCount)
			}
		}
	}
	sort.Ints(sizes)
	report.P50Bytes = nearestRank(sizes, 50)
	report.P95Bytes = nearestRank(sizes, 95)
	report.P99Bytes = nearestRank(sizes, 99)
	report.MaximumBytes = sizes[len(sizes)-1]
	return report, nil
}

func nearestRank(sorted []int, percentile int) int {
	index := (percentile*len(sorted) + 99) / 100
	return sorted[max(index-1, 0)]
}
