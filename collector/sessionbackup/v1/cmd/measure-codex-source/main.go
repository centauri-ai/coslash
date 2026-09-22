package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/codex"
)

type report struct {
	SchemaVersion         string `json:"schemaVersion"`
	CollectorVersion      string `json:"collectorVersion"`
	FamilyCount           int    `json:"familyCount"`
	RawArtifactCount      int    `json:"rawArtifactCount"`
	UnreadableCount       int    `json:"unreadableCount"`
	TotalRawBytes         int64  `json:"totalRawBytes"`
	P50FamilyRawBytes     int64  `json:"p50FamilyRawBytes"`
	P95FamilyRawBytes     int64  `json:"p95FamilyRawBytes"`
	P99FamilyRawBytes     int64  `json:"p99FamilyRawBytes"`
	MaximumFamilyRawBytes int64  `json:"maximumFamilyRawBytes"`
	MaximumArtifactBytes  int64  `json:"maximumArtifactBytes"`
}

func main() {
	version := flag.String("collector-version", "", "collector revision measured")
	root := flag.String("root", "", "approved Codex sessions root")
	archivedRoot := flag.String("archived-root", "", "approved Codex archived_sessions root")
	flag.Parse()
	if *version == "" || flag.NArg() != 0 {
		fail()
	}
	if *root == "" {
		var err error
		*root, err = codex.Root()
		if err != nil {
			fail()
		}
	}
	result := report{SchemaVersion: "session-backup-source-measurement/v1", CollectorVersion: *version}
	files := []string{}
	for _, scanRoot := range codexRoots(*root, *archivedRoot) {
		scan, err := codex.ScanSource(vendors.LocalReadSource, scanRoot)
		if err != nil {
			fail()
		}
		result.UnreadableCount += scan.SkippedTotal
		files = append(files, scan.Files...)
	}
	headers := codex.HeadersSource(vendors.LocalReadSource, files)
	roots := codex.FamilyRoots(headers)
	families := map[string]int64{}
	for _, name := range files {
		familyID, ok := roots[name]
		info, statErr := vendors.LocalReadSource.Stat(name)
		if !ok || headers[name].Err != nil || statErr != nil || info.Size() < 0 {
			result.UnreadableCount++
			continue
		}
		families[familyID] += info.Size()
		result.RawArtifactCount++
		result.TotalRawBytes += info.Size()
		result.MaximumArtifactBytes = max(result.MaximumArtifactBytes, info.Size())
	}
	sizes := make([]int64, 0, len(families))
	for _, size := range families {
		sizes = append(sizes, size)
	}
	if len(sizes) == 0 {
		fail()
	}
	sort.Slice(sizes, func(i, j int) bool { return sizes[i] < sizes[j] })
	result.FamilyCount = len(sizes)
	result.P50FamilyRawBytes = nearestRank(sizes, 50)
	result.P95FamilyRawBytes = nearestRank(sizes, 95)
	result.P99FamilyRawBytes = nearestRank(sizes, 99)
	result.MaximumFamilyRawBytes = sizes[len(sizes)-1]
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if encoder.Encode(result) != nil {
		os.Exit(1)
	}
	if !successful(result) {
		os.Exit(1)
	}
}

func successful(result report) bool {
	return result.UnreadableCount == 0
}

func codexRoots(root, archivedRoot string) []string {
	if archivedRoot == "" {
		archivedRoot = filepath.Join(filepath.Dir(root), "archived_sessions")
	}
	return []string{root, archivedRoot}
}

func nearestRank(sorted []int64, percentile int) int64 {
	index := (percentile*len(sorted) + 99) / 100
	return sorted[max(index-1, 0)]
}

func fail() {
	fmt.Fprintln(os.Stderr, "measurement failed")
	os.Exit(1)
}
