package main

import (
	"context"
	"os/exec"
	"sync"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/launch"
	"github.com/centauri-ai/coslash/collector/internal/settings"
	"github.com/centauri-ai/coslash/collector/internal/vendors/opencode"
)

const settingsOptionsTTL = time.Minute
const settingsOptionsTimeout = 30 * time.Second

type settingsOptions struct {
	SynthesisBackends []availableBackend  `json:"synthesisBackends"`
	Terminals         []availableTerminal `json:"terminals"`
	Reviewers         []availableReviewer `json:"reviewers"`
	Refreshing        bool                `json:"refreshing"`
}

func defaultSettingsOptions() settingsOptions {
	options := settingsOptions{}
	for _, option := range settings.BackendOptions() {
		if option.ID == settings.BackendOpenCode {
			option.Models = openCodeModels(nil)
		}
		options.SynthesisBackends = append(options.SynthesisBackends, availableBackend{BackendOption: option})
	}
	for _, option := range settings.TerminalOptions() {
		options.Terminals = append(options.Terminals, availableTerminal{TerminalOption: option})
	}
	// ReviewerOptions resolves Cursor's executable; defer it to discovery too.
	options.Reviewers = []availableReviewer{}
	return options
}

func discoverSettingsOptions(ctx context.Context) settingsOptions {
	options := defaultSettingsOptions()
	for i := range options.SynthesisBackends {
		option := &options.SynthesisBackends[i]
		_, err := exec.LookPath(settings.BackendExecutable(option.ID))
		option.Available = err == nil
		if option.ID == settings.BackendOpenCode && option.Available {
			option.Models = openCodeModels(opencode.SynthesisModels(ctx))
		}
	}
	for i := range options.Terminals {
		options.Terminals[i].Available = launch.AvailableContext(ctx, options.Terminals[i].ID)
	}
	for _, option := range launch.ReviewerOptions() {
		options.Reviewers = append(options.Reviewers, availableReviewer{ID: option.ID, Label: option.Label, Available: launch.ReviewCLIAvailable(ctx, option.ID)})
	}
	return options
}

// Requests read immutable options while one service-owned refresh does CLI work.
type settingsOptionsCache struct {
	mu          sync.Mutex
	ctx         context.Context
	cancel      context.CancelFunc
	options     settingsOptions
	loaded      time.Time
	running     chan struct{}
	now         func() time.Time
	withTimeout func(context.Context, time.Duration) (context.Context, context.CancelFunc)
	discover    func(context.Context) settingsOptions
}

func newSettingsOptionsCache() *settingsOptionsCache {
	ctx, cancel := context.WithCancel(context.Background())
	return &settingsOptionsCache{ctx: ctx, cancel: cancel, options: defaultSettingsOptions(), now: time.Now, withTimeout: context.WithTimeout, discover: discoverSettingsOptions}
}

func (cache *settingsOptionsCache) snapshot() settingsOptions {
	if cache == nil {
		return defaultSettingsOptions()
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.ctx.Err() == nil && cache.running == nil && (cache.loaded.IsZero() || cache.now().Sub(cache.loaded) >= settingsOptionsTTL) {
		cache.running = make(chan struct{})
		go cache.refresh(cache.running)
	}
	options := cache.options
	options.Refreshing = cache.running != nil && cache.ctx.Err() == nil
	return options
}

func (cache *settingsOptionsCache) refresh(done chan struct{}) {
	ctx, cancel := cache.withTimeout(cache.ctx, settingsOptionsTimeout)
	defer cancel()
	options := cache.discover(ctx)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.ctx.Err() == nil && ctx.Err() == nil {
		cache.options = options
		cache.loaded = cache.now()
	}
	cache.running = nil
	close(done)
}

func (cache *settingsOptionsCache) close() {
	cache.mu.Lock()
	cache.cancel()
	done := cache.running
	cache.mu.Unlock()
	if done != nil {
		<-done
	}
}
