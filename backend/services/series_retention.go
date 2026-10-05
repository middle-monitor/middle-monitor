package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

// Measured on prod: an unthrottled delete over the series indices ran at about
// 5 200 documents a second and held OpenSearch at 450% CPU. At 1 000 a second
// CPU looked fine but the scroll read 267 MB/s from disk (OpenSearch has 1.5 GB,
// so the indices are not cached) and iowait reached 63%; at 200 iowait fell to
// 3%. A plan downgrade can expire hundreds of millions of points at once, so
// the series purge always runs at the pace the disk takes.
const seriesRetentionRPS = 200

// seriesDeleteRunning reports whether a delete_by_query over the series indices
// is still in progress. A slow purge outlives the cleanup run that started it,
// and each worker start and nightly run would otherwise stack another one.
func (s *OpenSearchService) seriesDeleteRunning() (bool, error) {
	url := fmt.Sprintf("%s/_tasks?actions=*byquery&detailed=true", s.baseURL)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return false, err
	}
	if s.username != "" && s.password != "" {
		req.SetBasicAuth(s.username, s.password)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return false, newOpenSearchStatusError("list tasks", resp)
	}
	var result struct {
		Nodes map[string]struct {
			Tasks map[string]struct {
				Description string `json:"description"`
			} `json:"tasks"`
		} `json:"nodes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, err
	}
	for _, node := range result.Nodes {
		for _, task := range node.Tasks {
			if strings.Contains(task.Description, SeriesIndexPrefix) {
				return true, nil
			}
		}
	}
	return false, nil
}

// startSeriesRetention launches the series purge as a throttled background
// task and logs its id; it does not wait for the purge to end.
func (s *OpenSearchService) startSeriesRetention(query map[string]interface{}) {
	running, err := s.seriesDeleteRunning()
	if err != nil {
		// Unknown is treated as busy: a duplicate purge is what took prod down.
		slog.Error("series retention skipped, task list unavailable", "error", err)
		return
	}
	if running {
		slog.Info("series retention already running, not starting another")
		return
	}

	body, err := json.Marshal(map[string]interface{}{"query": query})
	if err != nil {
		slog.Error("series retention marshal failed", "error", err)
		return
	}
	url := fmt.Sprintf("%s/%s/_delete_by_query?wait_for_completion=false&conflicts=proceed&requests_per_second=%d",
		s.baseURL, SeriesIndexPattern, seriesRetentionRPS)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		slog.Error("series retention request failed", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if s.username != "" && s.password != "" {
		req.SetBasicAuth(s.username, s.password)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		slog.Error("series retention failed", "error", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		slog.Error("series retention refused", "error", newOpenSearchStatusError("series retention", resp))
		return
	}
	var started struct {
		Task string `json:"task"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&started)
	slog.Info("series retention started", "task", started.Task, "requests_per_second", seriesRetentionRPS)
}
