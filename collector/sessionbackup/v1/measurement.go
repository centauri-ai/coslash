package sessionbackupv1

import (
	"fmt"
	"math"
	"sort"
)

type MeasurementReport struct {
	SchemaVersion    string          `json:"schemaVersion"`
	CollectorVersion string          `json:"collectorVersion"`
	BundleCount      int             `json:"bundleCount"`
	ArtifactCount    int             `json:"artifactCount"`
	ArtifactCounts   []ArtifactCount `json:"artifactCounts"`
	P50Bytes         int64           `json:"p50Bytes"`
	P95Bytes         int64           `json:"p95Bytes"`
	P99Bytes         int64           `json:"p99Bytes"`
	MaximumBytes     int64           `json:"maximumBytes"`
	MaximumArtifact  int64           `json:"maximumArtifactBytes"`
}

type Measurer struct {
	report MeasurementReport
	sizes  []int64
	counts map[string]int
}

func NewMeasurer(collectorVersion string) (*Measurer, error) {
	if !identifier(collectorVersion) {
		return nil, fmt.Errorf("collector version is required")
	}
	return &Measurer{
		report: MeasurementReport{SchemaVersion: SchemaVersion, CollectorVersion: collectorVersion},
		counts: map[string]int{},
	}, nil
}

func (m *Measurer) Add(manifest Manifest) error {
	manifestBytes, err := Marshal(manifest)
	if err != nil {
		return err
	}
	if manifest.Summary.TotalBytes > math.MaxInt64-int64(len(manifestBytes)) {
		return fmt.Errorf("%w: bundle byte total overflow", ErrInvalid)
	}
	m.report.BundleCount++
	m.report.ArtifactCount += len(manifest.Artifacts)
	m.sizes = append(m.sizes, manifest.Summary.TotalBytes+int64(len(manifestBytes)))
	for _, artifact := range manifest.Artifacts {
		m.counts[artifact.Kind]++
		m.report.MaximumArtifact = max(m.report.MaximumArtifact, artifact.ByteLength)
	}
	return nil
}

func (m *Measurer) Report() (MeasurementReport, error) {
	if len(m.sizes) == 0 {
		return MeasurementReport{}, fmt.Errorf("measurement corpus is empty")
	}
	sort.Slice(m.sizes, func(i, j int) bool { return m.sizes[i] < m.sizes[j] })
	report := m.report
	report.P50Bytes = nearestRank(m.sizes, 50)
	report.P95Bytes = nearestRank(m.sizes, 95)
	report.P99Bytes = nearestRank(m.sizes, 99)
	report.MaximumBytes = m.sizes[len(m.sizes)-1]
	kinds := make([]string, 0, len(m.counts))
	for kind := range m.counts {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		report.ArtifactCounts = append(report.ArtifactCounts, ArtifactCount{Kind: kind, Count: m.counts[kind]})
	}
	return report, nil
}

func nearestRank(sorted []int64, percentile int) int64 {
	index := (percentile*len(sorted) + 99) / 100
	return sorted[max(index-1, 0)]
}
