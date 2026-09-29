package hubclient

import (
	"context"
	"errors"
	"net/http"
	"time"
)

type V4ListItem struct {
	V4Session
	ActivityAt   time.Time `json:"activityAt"`
	ContentBytes int64     `json:"contentBytes"`
}

type V4ListResult struct {
	LocalKeyHash string `json:"localKeyHash"`
	SessionID    string `json:"sessionId,omitempty"`
	State        string `json:"state"`
	Code         string `json:"code,omitempty"`
}

func (c *Client) V4ListBatch(ctx context.Context, items []V4ListItem) ([]V4ListResult, error) {
	if len(items) < 1 || len(items) > 50 {
		return nil, errors.New("v4 listing batch must contain 1 to 50 items")
	}
	var result struct {
		Results []V4ListResult `json:"results"`
	}
	err := c.v4Request(ctx, http.MethodPost, "/v4/sessions:list-batch", struct {
		Items []V4ListItem `json:"items"`
	}{items}, &result)
	return result.Results, err
}
