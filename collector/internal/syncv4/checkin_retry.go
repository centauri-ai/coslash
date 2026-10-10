package syncv4

import (
	"errors"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

const (
	checkInRejectionInitialBackoff = time.Minute
	checkInRejectionMaxBackoff     = time.Hour
)

type checkInBackoffError struct {
	problem hubclient.V4Problem
	cause   error
}

func (err checkInBackoffError) Error() string { return err.cause.Error() }
func (err checkInBackoffError) Unwrap() error { return err.cause }
func (err checkInBackoffError) As(target any) bool {
	problem, ok := target.(*hubclient.V4Problem)
	if !ok {
		return false
	}
	*problem = err.problem
	return true
}

func permanentCheckInRejection(problem hubclient.V4Problem) bool {
	if problem.HTTPStatus < 400 || problem.HTTPStatus >= 500 {
		return false
	}
	switch problem.HTTPStatus {
	case 401, 408, 425, 429:
		return false
	}
	switch problem.Code {
	case "device_dormant", "rate_limited", "sync_paused", "device_sync_off", "unauthorized":
		return false
	}
	return true
}

func checkInRejectionBackoff(attempt int) time.Duration {
	delay := checkInRejectionInitialBackoff
	for attempt > 1 && delay < checkInRejectionMaxBackoff {
		delay *= 2
		attempt--
	}
	return min(delay, checkInRejectionMaxBackoff)
}

func (r *Runner) recordCheckInFailure(err error) error {
	var problem hubclient.V4Problem
	if !errors.As(err, &problem) {
		return err
	}

	retryAfter := problem.RetryAfter
	if permanentCheckInRejection(problem) {
		backoff := checkInRejectionBackoff(int(r.checkInRejections.Add(1)))
		if backoff > retryAfter {
			retryAfter = backoff
		}
	}
	if retryAfter <= 0 {
		return err
	}
	r.checkInRetryUntil.Store(r.now().Add(retryAfter).UnixNano())
	if problem.RetryAfter == retryAfter {
		return err
	}
	problem.RetryAfter = retryAfter
	return checkInBackoffError{problem: problem, cause: err}
}

func (r *Runner) resetCheckInBackoff() {
	r.checkInRetryUntil.Store(0)
	r.checkInRejections.Store(0)
}
