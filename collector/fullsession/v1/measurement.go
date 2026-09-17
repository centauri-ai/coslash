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

// Measurer accumulates corpus statistics without retaining decoded records or
// their change bodies.
type Measurer struct {
	report MeasurementReport
	sizes  []int
}

// NewMeasurer starts an incremental measurement for one collector version.
func NewMeasurer(collectorVersion string) (*Measurer, error) {
	if collectorVersion == "" {
		return nil, fmt.Errorf("collector version is required")
	}
	return &Measurer{report: MeasurementReport{
		SchemaVersion: SchemaVersion, CollectorVersion: collectorVersion,
	}}, nil
}

// Add validates and incorporates one record without retaining it.
func (m *Measurer) Add(record Record) error {
	data, err := Marshal(record)
	if err != nil {
		return err
	}
	m.report.CorpusSize++
	m.sizes = append(m.sizes, len(data))
	for _, edit := range record.Session.FileEdits {
		for _, change := range edit.Changes {
			m.report.TotalChangeBodies++
			m.report.MaximumChangeBytes = max(m.report.MaximumChangeBytes, change.ByteCount)
		}
	}
	return nil
}

// Report returns the completed corpus statistics.
func (m *Measurer) Report() (MeasurementReport, error) {
	if len(m.sizes) == 0 {
		return m.report, fmt.Errorf("measurement corpus is empty")
	}
	sort.Ints(m.sizes)
	report := m.report
	report.P50Bytes = nearestRank(m.sizes, 50)
	report.P95Bytes = nearestRank(m.sizes, 95)
	report.P99Bytes = nearestRank(m.sizes, 99)
	report.MaximumBytes = m.sizes[len(m.sizes)-1]
	return report, nil
}

func Measure(records []Record, collectorVersion string) (MeasurementReport, error) {
	measurer, err := NewMeasurer(collectorVersion)
	if err != nil {
		return MeasurementReport{}, err
	}
	for index, record := range records {
		if err := measurer.Add(record); err != nil {
			return MeasurementReport{}, fmt.Errorf("measure record %d: %w", index, err)
		}
	}
	return measurer.Report()
}

func nearestRank(sorted []int, percentile int) int {
	index := (percentile*len(sorted) + 99) / 100
	return sorted[max(index-1, 0)]
}
