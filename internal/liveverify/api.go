package liveverify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// apiClient speaks to the installed API on loopback with the configured token.
type apiClient struct {
	base  string
	token string
	http  *http.Client
}

func newAPIClient(addr, token string) *apiClient {
	return &apiClient{base: "http://" + addr + "/api/v1", token: token, http: &http.Client{Timeout: 30 * time.Second}}
}

type (
	apiSettings struct {
		AutoProcessing bool `json:"auto_processing"`
		ResidentWorker bool `json:"resident_worker"`
	}
	apiCall struct {
		ID          int64  `json:"id"`
		JobID       *int64 `json:"job_id"`
		Role        string `json:"role"`
		OK          bool   `json:"ok"`
		FailureKind string `json:"failure_kind"`
	}
	apiUnit struct {
		Stage string `json:"stage"`
		JobID *int64 `json:"job_id"`
	}
	apiStatus struct {
		AgentCalls []apiCall `json:"agent_calls"`
		InFlight   []apiUnit `json:"in_flight"`
	}
	apiJob struct {
		ID           int64  `json:"id"`
		ProcessState string `json:"process_state"`
	}
	apiRun struct {
		ID         int64          `json:"id"`
		Trigger    string         `json:"trigger"`
		State      string         `json:"state"`
		Stats      map[string]int `json:"stats"`
		Error      *string        `json:"error"`
		FinishedAt *time.Time     `json:"finished_at"`
	}
	apiAttempt struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
		Calls  []struct {
			Role string `json:"role"`
			OK   bool   `json:"ok"`
		} `json:"calls"`
	}
	apiError struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
)

// do issues one request and returns the status and body. A transport failure is
// an error; any HTTP status is returned for the caller to judge.
func (c *apiClient) do(ctx context.Context, method, path string, body any, authenticated bool) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return 0, nil, err
	}
	if authenticated {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	return response.StatusCode, data, err
}

// getJSON reads one authenticated resource that must answer 200.
func (c *apiClient) getJSON(ctx context.Context, path string, into any) error {
	status, data, err := c.do(ctx, http.MethodGet, path, nil, true)
	if err != nil {
		return fmt.Errorf("API GET %s: %w", path, err)
	}
	if status != http.StatusOK {
		return fmt.Errorf("API GET %s answered HTTP %d", path, status)
	}
	if err := json.Unmarshal(data, into); err != nil {
		return fmt.Errorf("API GET %s: decode: %w", path, err)
	}
	return nil
}

func (c *apiClient) responding(ctx context.Context) bool {
	status, _, err := c.do(ctx, http.MethodGet, "/settings", nil, true)
	return err == nil && status == http.StatusOK
}

// waitResponding gives a freshly started service time to listen.
func (c *apiClient) waitResponding(ctx context.Context) bool {
	return waitUntil(ctx, time.Minute, time.Second, func() bool { return c.responding(ctx) })
}

func (c *apiClient) settings(ctx context.Context) (apiSettings, error) {
	var value apiSettings
	return value, c.getJSON(ctx, "/settings", &value)
}

func (c *apiClient) setAutoProcessing(ctx context.Context, enabled bool) error {
	status, data, err := c.do(ctx, http.MethodPut, "/settings", map[string]bool{"auto_processing": enabled}, true)
	if err != nil {
		return err
	}
	var value apiSettings
	if status != http.StatusOK || json.Unmarshal(data, &value) != nil || value.AutoProcessing != enabled {
		return fmt.Errorf("PUT /settings answered HTTP %d", status)
	}
	return nil
}

func (c *apiClient) status(ctx context.Context) (apiStatus, error) {
	var value apiStatus
	return value, c.getJSON(ctx, "/status", &value)
}

func (c *apiClient) job(ctx context.Context, id int64) (apiJob, error) {
	var value apiJob
	return value, c.getJSON(ctx, fmt.Sprintf("/jobs/%d", id), &value)
}

func (c *apiClient) jobIDs(ctx context.Context, state string, limit int) ([]int64, error) {
	var page struct {
		Items []apiJob `json:"items"`
	}
	if err := c.getJSON(ctx, fmt.Sprintf("/jobs?process_state=%s&limit=%d", state, limit), &page); err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(page.Items))
	for _, item := range page.Items {
		ids = append(ids, item.ID)
	}
	return ids, nil
}

func (c *apiClient) runs(ctx context.Context, limit int) ([]apiRun, error) {
	var page struct {
		Items []apiRun `json:"items"`
	}
	return page.Items, c.getJSON(ctx, fmt.Sprintf("/runs?limit=%d", limit), &page)
}

func (c *apiClient) attempts(ctx context.Context, job int64) ([]apiAttempt, error) {
	var history struct {
		Attempts []apiAttempt `json:"attempts"`
	}
	return history.Attempts, c.getJSON(ctx, fmt.Sprintf("/jobs/%d/letter-history", job), &history)
}

// post issues one authenticated POST and returns the status, the error code of
// a refusal, and the raw body.
func (c *apiClient) post(ctx context.Context, path string) (int, string, []byte, error) {
	status, data, err := c.do(ctx, http.MethodPost, path, nil, true)
	if err != nil {
		return 0, "", nil, err
	}
	var refusal apiError
	_ = json.Unmarshal(data, &refusal)
	return status, refusal.Error.Code, data, nil
}

// waitUntil polls a condition until it holds, the budget runs out or the
// context ends.
func waitUntil(ctx context.Context, budget, interval time.Duration, condition func() bool) bool {
	deadline := time.Now().Add(budget)
	for {
		if condition() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(interval):
		}
	}
}
