package remoteviewv1

import (
	"errors"
	"fmt"
	"slices"
)

// FitView keeps newest whole sessions until the encoded view fits under the
// aggregate limit. Dropped sessions set truncated with payload_limit when the
// input was not already marked truncated for another reason.
func FitView(view View) (View, error) {
	if view.Sessions == nil {
		view.Sessions = []Session{}
	}
	sessions := slices.Clone(view.Sessions)
	slices.SortFunc(sessions, func(a, b Session) int {
		switch {
		case a.LastActivityAtMs > b.LastActivityAtMs:
			return -1
		case a.LastActivityAtMs < b.LastActivityAtMs:
			return 1
		case a.SourceSessionID < b.SourceSessionID:
			return -1
		case a.SourceSessionID > b.SourceSessionID:
			return 1
		default:
			return 0
		}
	})
	view.Sessions = sessions
	if _, err := Marshal(view); err == nil {
		return view, nil
	} else if !errors.Is(err, ErrOversized) {
		return View{}, err
	}
	for len(view.Sessions) > 0 {
		view.Sessions = view.Sessions[:len(view.Sessions)-1]
		markPayloadTruncated(&view)
		if _, err := Marshal(view); err == nil {
			return view, nil
		} else if !errors.Is(err, ErrOversized) {
			return View{}, err
		}
	}
	empty := view
	empty.Sessions = []Session{}
	markPayloadTruncated(&empty)
	if _, err := Marshal(empty); err != nil {
		return View{}, fmt.Errorf("empty remote view exceeds aggregate limit: %w", err)
	}
	return empty, nil
}

func markPayloadTruncated(view *View) {
	if view.Truncated && view.TruncationReason != nil {
		return
	}
	view.Truncated = true
	reason := TruncationReasonPayload
	view.TruncationReason = &reason
}
