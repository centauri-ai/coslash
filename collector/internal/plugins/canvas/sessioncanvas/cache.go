package sessioncanvas

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"unicode/utf8"

	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/contracts"
	"github.com/centauri-ai/coslash/collector/internal/plugins/canvas/sessiondetail"
)

type analysisCache struct {
	mu       sync.Mutex
	capacity int
	order    []string
	values   map[string]TurnAnalysis
}

func newAnalysisCache(capacity int) *analysisCache {
	return &analysisCache{capacity: capacity, values: map[string]TurnAnalysis{}}
}

func (cache *analysisCache) get(key string) (TurnAnalysis, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	value, found := cache.values[key]
	return value, found
}

func (cache *analysisCache) put(key string, value TurnAnalysis) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if _, exists := cache.values[key]; exists {
		cache.values[key] = value
		return
	}
	if len(cache.order) == cache.capacity {
		delete(cache.values, cache.order[0])
		cache.order = cache.order[1:]
	}
	cache.order = append(cache.order, key)
	cache.values[key] = value
}

func turnCacheKey(identity contracts.SessionIdentity, turn sessiondetail.Turn) string {
	encoded, _ := json.Marshal(struct {
		Session contracts.SessionIdentity `json:"session"`
		Turn    sessiondetail.Turn        `json:"turn"`
	}{identity, turn})
	digest := sha256.Sum256(encoded)
	return "turn-v1-" + hex.EncodeToString(digest[:])
}

func validateAnalysis(result TurnAnalysis) error {
	if result.Intention == "" && result.PlanSummary == "" && result.Status == "" && len(result.Findings) == 0 && len(result.Issues) == 0 {
		return ErrAnalysisFailed
	}
	if len(result.Intention) > 1000 || len(result.PlanSummary) > 2000 || len(result.Status) > 2000 || len(result.Findings) > 4 || len(result.Issues) > 4 {
		return ErrAnalysisFailed
	}
	for _, list := range [][]string{result.Findings, result.Issues} {
		for _, item := range list {
			if item == "" || len(item) > 1000 || !utf8.ValidString(item) {
				return ErrAnalysisFailed
			}
		}
	}
	return nil
}
