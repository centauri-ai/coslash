package syncv4

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/sessionbackupproducer"
)

type chunkJob struct {
	missing hubclient.V4Missing
	name    string
}

func retryableChunk(err error) bool {
	var problem hubclient.V4Problem
	if !errors.As(err, &problem) {
		return false
	}
	return problem.HTTPStatus == 429 || problem.HTTPStatus >= 500 || problem.Code == "rate_limited" || strings.HasPrefix(problem.Code, "http_5")
}

func (r *Runner) putChunk(ctx context.Context, reader *sessionbackupproducer.BundleReader, uploadID string, job chunkJob) (error, bool) {
	if err := ctx.Err(); err != nil {
		return err, false
	}
	body, err := readChunk(reader, job.name, job.missing.Offset, job.missing.Bytes)
	if err != nil {
		return err, false
	}
	throttled := false
	for attempt := 0; attempt < 3; attempt++ {
		err = r.Hub.V4PutChunk(ctx, uploadID, job.missing, bytes.NewReader(body))
		throttled = throttled || retryableChunk(err)
		if err == nil || !r.scaleEnabled || !retryableChunk(err) {
			return err, throttled
		}
		timer := time.NewTimer(time.Duration(1<<attempt) * 200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err(), throttled
		case <-timer.C:
		}
	}
	return err, throttled
}

func (r *Runner) putChunkGroup(ctx context.Context, reader *sessionbackupproducer.BundleReader, uploadID string, jobs []chunkJob) ([]hubclient.V4Missing, error) {
	maxWorkers := 1
	if r.scaleEnabled {
		maxWorkers = 4
	}
	workers := r.chunkWorkers
	if workers <= 0 {
		workers = maxWorkers
	}
	workers = min(workers, maxWorkers, len(jobs))
	results := make([]error, len(jobs))
	throttles := make([]bool, len(jobs))
	indices := make(chan int)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			for i := range indices {
				results[i], throttles[i] = r.putChunk(ctx, reader, uploadID, jobs[i])
			}
		}()
	}
	for i := range jobs {
		indices <- i
	}
	close(indices)
	group.Wait()
	var sent []hubclient.V4Missing
	var firstErr error
	throttled := false
	for i, err := range results {
		throttled = throttled || throttles[i]
		if err == nil {
			sent = append(sent, jobs[i].missing)
		} else {
			if firstErr == nil {
				firstErr = err
			}
			throttled = throttled || retryableChunk(err)
		}
	}
	if throttled {
		r.chunkWorkers = max(1, workers/2)
	} else if firstErr == nil {
		r.chunkWorkers = min(maxWorkers, workers+1)
	}
	return sent, firstErr
}

func (r *Runner) confirmChunks(ctx context.Context, uploadID string, sent []hubclient.V4Missing) error {
	var batches [][]hubclient.V4Missing
	for len(sent) > 0 {
		count, size := 0, int64(0)
		for count < len(sent) && count < hubclient.V4MaxConfirm && (size == 0 || size+sent[count].Bytes <= chunkBytes) {
			size += sent[count].Bytes
			count++
		}
		if count == 0 {
			return io.ErrShortBuffer
		}
		batches = append(batches, sent[:count])
		sent = sent[count:]
	}
	if !r.scaleEnabled || len(batches) < 2 {
		for _, batch := range batches {
			if _, err := r.Hub.V4Confirm(ctx, uploadID, batch...); err != nil {
				return err
			}
		}
		return nil
	}
	results := make([]error, len(batches))
	indices := make(chan int)
	var group sync.WaitGroup
	for range min(4, len(batches)) {
		group.Add(1)
		go func() {
			defer group.Done()
			for i := range indices {
				_, results[i] = r.Hub.V4Confirm(ctx, uploadID, batches[i]...)
			}
		}()
	}
	for i := range batches {
		indices <- i
	}
	close(indices)
	group.Wait()
	for _, err := range results {
		if err != nil {
			return err
		}
	}
	return nil
}
