package prometheus

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

type Client interface {
	Query(ctx context.Context, expr string) (float64, error)
}

type HTTPClient struct {
	baseURL string
	http    *http.Client
	mu      sync.RWMutex
	cache   map[string]cacheEntry
	ttl     time.Duration
}

type cacheEntry struct {
	value    float64
	cachedAt time.Time
}

func New(baseURL string) *HTTPClient {
	return &HTTPClient{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 7 * time.Second},
		cache:   make(map[string]cacheEntry),
		ttl:     5 * time.Second,
	}
}

func (c *HTTPClient) Query(ctx context.Context, expr string) (float64, error) {
	c.mu.RLock()
	entry, ok := c.cache[expr]
	c.mu.RUnlock()
	if ok && time.Since(entry.cachedAt) < c.ttl {
		return entry.value, nil
	}

	u, err := url.Parse(c.baseURL)
	if err != nil {
		return 0, fmt.Errorf("parse prometheus url: %w", err)
	}
	u.Path = "/api/v1/query"
	q := u.Query()
	q.Set("query", expr)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var parsed queryResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return 0, fmt.Errorf("decode prometheus response: %w", err)
	}
	if parsed.Status != "success" {
		return 0, fmt.Errorf("prometheus query failed: %s", parsed.Error)
	}
	if len(parsed.Data.Result) == 0 || len(parsed.Data.Result[0].Value) < 2 {
		return 0, fmt.Errorf("prometheus query returned no data")
	}
	valueRaw, ok := parsed.Data.Result[0].Value[1].(string)
	if !ok {
		return 0, fmt.Errorf("unexpected prometheus value type")
	}
	value, err := strconv.ParseFloat(valueRaw, 64)
	if err != nil {
		return 0, fmt.Errorf("parse prometheus value: %w", err)
	}
	c.mu.Lock()
	c.cache[expr] = cacheEntry{value: value, cachedAt: time.Now().UTC()}
	c.mu.Unlock()
	return value, nil
}

type queryResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []interface{}     `json:"value"`
		} `json:"result"`
	} `json:"data"`
	Error string `json:"error"`
}
