// Package pricing estimates token costs from OpenRouter's public model catalog.
package pricing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

const ModelsURL = "https://openrouter.ai/api/v1/models"

type Rates struct {
	Prompt     string `json:"prompt"`
	Completion string `json:"completion"`
	CacheRead  string `json:"input_cache_read"`
	CacheWrite string `json:"input_cache_write"`
	Request    string `json:"request"`
	Overrides  []Tier `json:"overrides"`
}
type Tier struct {
	MinPrompt uint64 `json:"min_prompt_tokens"`
	Rates
}
type Model struct {
	ID      string `json:"id"`
	Pricing Rates  `json:"pricing"`
}
type Catalog struct {
	Data []Model `json:"data"`
}
type Client struct {
	URL            string
	HTTP           *http.Client
	mu             sync.Mutex
	catalog        Catalog
	fetched, retry time.Time
}

func NewClient() *Client {
	return &Client{URL: ModelsURL, HTTP: &http.Client{Timeout: 15 * time.Second}}
}
func (c *Client) Load(ctx context.Context) (Catalog, time.Time, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now().UTC()
	if !c.fetched.IsZero() && now.Sub(c.fetched) < 6*time.Hour {
		return c.catalog, c.fetched, nil
	}
	if now.Before(c.retry) {
		if !c.fetched.IsZero() {
			return c.catalog, c.fetched, nil
		}
		return Catalog{}, time.Time{}, fmt.Errorf("price catalog retry pending")
	}
	c.retry = now.Add(5 * time.Minute)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return Catalog{}, time.Time{}, err
	}
	response, err := c.HTTP.Do(req)
	if err == nil {
		defer response.Body.Close()
		if response.StatusCode != 200 {
			err = fmt.Errorf("price catalog HTTP %d", response.StatusCode)
		} else {
			var catalog Catalog
			err = json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&catalog)
			if err == nil && len(catalog.Data) > 0 {
				c.catalog = catalog
				c.fetched = now
				return catalog, now, nil
			}
			if err == nil {
				err = fmt.Errorf("empty price catalog")
			}
		}
	}
	if !c.fetched.IsZero() {
		return c.catalog, c.fetched, nil
	}
	return Catalog{}, time.Time{}, err
}

// Explicit runtime aliases. Unknown variants are deliberately not fuzzy-matched.
var aliases = map[string]string{"grok-4.6-build": "x-ai/grok-4.6", "gemini-3.7-flash-high": "google/gemini-3.7-flash", "claude-opus-4-6-thinking": "anthropic/claude-opus-4.6"}

func (c Catalog) Match(name string) (Model, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if alias, ok := aliases[name]; ok {
		name = alias
	}
	var candidate Model
	count := 0
	for _, m := range c.Data {
		id := strings.ToLower(m.ID)
		if id == name {
			return m, true
		}
		if _, suffix, ok := strings.Cut(id, "/"); ok && suffix == name {
			candidate = m
			count++
		}
	}
	return candidate, count == 1
}

type Usage struct {
	Input, Output, CacheRead, CacheWrite uint64
	InputIncludesCache                   bool
}

// Output already contains reasoning tokens. Never charge that subset twice.
func Estimate(model Model, u Usage) (string, bool) {
	prompt := u.Input
	if u.InputIncludesCache {
		if u.CacheRead > prompt || u.CacheWrite > prompt-u.CacheRead {
			return "", false
		}
		prompt -= u.CacheRead + u.CacheWrite
	}
	contextTokens := prompt + u.CacheRead + u.CacheWrite
	rates := model.Pricing
	var threshold uint64
	for _, tier := range model.Pricing.Overrides {
		if contextTokens >= tier.MinPrompt && tier.MinPrompt >= threshold {
			threshold = tier.MinPrompt
			if tier.Prompt != "" {
				rates.Prompt = tier.Prompt
			}
			if tier.Completion != "" {
				rates.Completion = tier.Completion
			}
			if tier.CacheRead != "" {
				rates.CacheRead = tier.CacheRead
			}
			if tier.CacheWrite != "" {
				rates.CacheWrite = tier.CacheWrite
			}
		}
	}
	// When a provider publishes no separate cache rate, use the ordinary prompt rate.
	if rates.CacheRead == "" {
		rates.CacheRead = rates.Prompt
	}
	if rates.CacheWrite == "" {
		rates.CacheWrite = rates.Prompt
	}
	total := new(big.Rat)
	for _, part := range []struct {
		n    uint64
		rate string
	}{{prompt, rates.Prompt}, {u.Output, rates.Completion}, {u.CacheRead, rates.CacheRead}, {u.CacheWrite, rates.CacheWrite}} {
		if part.n == 0 {
			continue
		}
		rate, ok := new(big.Rat).SetString(part.rate)
		if !ok || rate.Sign() < 0 {
			return "", false
		}
		total.Add(total, new(big.Rat).Mul(rate, new(big.Rat).SetInt(new(big.Int).SetUint64(part.n))))
	}
	if rates.Request != "" {
		rate, ok := new(big.Rat).SetString(rates.Request)
		if !ok || rate.Sign() < 0 {
			return "", false
		}
		total.Add(total, rate)
	}
	if rates.Prompt == "" || rates.Completion == "" {
		return "", false
	}
	return total.FloatString(8), true
}
