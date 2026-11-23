package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	searchpkg "github.com/centauri-ai/coslash/collector/internal/search"
)

const searchUsage = "usage: coslash search <query> [--limit N] --json | coslash search --status [sourceId/agent/sessionId] --json"

func searchRequestPath(args []string) (string, error) {
	status, jsonOutput := false, false
	query := ""
	limit := 10
	limited := false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--json":
			jsonOutput = true
		case args[i] == "--status":
			status = true
		case args[i] == "--limit" && i+1 < len(args):
			i++
			parsed, err := strconv.Atoi(args[i])
			if err != nil || parsed < 1 || parsed > 2000 {
				return "", errors.New(searchUsage)
			}
			limit = parsed
			limited = true
		case strings.HasPrefix(args[i], "-"):
			return "", errors.New(searchUsage)
		case query == "":
			query = args[i]
		default:
			return "", errors.New(searchUsage)
		}
	}
	if !jsonOutput {
		return "", errors.New(searchUsage)
	}
	values := url.Values{}
	if status {
		if limited {
			return "", errors.New(searchUsage)
		}
		if query != "" {
			triple := strings.SplitN(query, "/", 3)
			if len(triple) != 3 || triple[0] == "" || triple[1] == "" || triple[2] == "" {
				return "", errors.New(searchUsage)
			}
			values.Set("source", triple[0])
			values.Set("agent", triple[1])
			values.Set("session", triple[2])
		}
		return "/api/search/status?" + values.Encode(), nil
	}
	parsed := searchpkg.ParseQuery(query)
	if len(parsed.Terms) == 0 {
		return "", errors.New("search requires content terms")
	}
	for key, filters := range parsed.Filters {
		if key != "repo" && key != "agent" && key != "since" {
			return "", fmt.Errorf("%s: is a browser-only filter", key)
		}
		if len(filters) != 1 {
			return "", fmt.Errorf("only one %s: filter is supported", key)
		}
		value := filters[0]
		if key == "since" {
			if epoch, err := strconv.ParseInt(value, 10, 64); err == nil && epoch >= 0 {
				value = strconv.FormatInt(epoch, 10)
			} else {
				date, err := time.Parse("2006-01-02", value)
				if err != nil {
					return "", errors.New("since: requires YYYY-MM-DD or epoch milliseconds")
				}
				value = strconv.FormatInt(date.UnixMilli(), 10)
			}
		}
		values.Set(key, value)
	}
	values.Set("q", query)
	values.Set("limit", strconv.Itoa(limit))
	return "/api/search?" + values.Encode(), nil
}

func runSearch(stdout io.Writer, args []string) error {
	path, err := searchRequestPath(args)
	if err != nil {
		return err
	}
	client, err := newLocalAPIClient()
	if err != nil {
		return err
	}
	data, err := client.request(http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	var result json.RawMessage
	if err := json.Unmarshal(data, &result); err != nil {
		return errors.New("invalid search response")
	}
	return json.NewEncoder(stdout).Encode(result)
}
