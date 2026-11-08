package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type apiWindow struct {
	Used    float64 `json:"used_percent"`
	Seconds *int    `json:"limit_window_seconds"`
	Reset   *int64  `json:"reset_at"`
}
type apiLimits struct {
	Primary   *apiWindow `json:"primary_window"`
	Secondary *apiWindow `json:"secondary_window"`
}

func tokenExpired(token string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var claims struct {
		Expires int64 `json:"exp"`
	}
	return json.Unmarshal(payload, &claims) == nil && claims.Expires > 0 && claims.Expires <= time.Now().Unix()
}

func convertWindow(w *apiWindow) *window {
	if w == nil {
		return nil
	}
	var minutes *int
	if w.Seconds != nil {
		n := (*w.Seconds + 59) / 60
		minutes = &n
	}
	return &window{UsedPercent: w.Used, Minutes: minutes, ResetsAt: w.Reset}
}
func readDirect() (json.RawMessage, json.RawMessage, error) {
	return readDirectURL("https://chatgpt.com/backend-api/wham/usage")
}

func readDirectURL(url string) (json.RawMessage, json.RawMessage, error) {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		dir, err := os.UserHomeDir()
		if err != nil {
			return nil, nil, err
		}
		home = filepath.Join(dir, ".codex")
	}
	data, err := os.ReadFile(filepath.Join(home, "auth.json"))
	if err != nil {
		return nil, nil, fmt.Errorf("read Codex auth.json: %w; login unavailable", err)
	}
	var auth struct {
		Access string `json:"access_token"`
		Tokens struct {
			Access  string `json:"access_token"`
			Account string `json:"account_id"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(data, &auth); err != nil {
		return nil, nil, fmt.Errorf("invalid Codex auth.json")
	}
	token := auth.Tokens.Access
	if token == "" {
		token = auth.Access
	}
	if token == "" {
		return nil, nil, fmt.Errorf("Codex access token missing; login unavailable")
	}
	if tokenExpired(token) {
		return nil, nil, fmt.Errorf("Codex login expired")
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("quota request failed: invalid URL")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if auth.Tokens.Account != "" {
		req.Header.Set("ChatGPT-Account-Id", auth.Tokens.Account)
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return fmt.Errorf("quota request redirected")
	}}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("quota request failed: network error")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, nil, fmt.Errorf("Codex login expired")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("quota request failed: HTTP %d", resp.StatusCode)
	}
	data, err = io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil {
		return nil, nil, fmt.Errorf("read quota response failed")
	}
	if len(data) > 4<<20 {
		return nil, nil, fmt.Errorf("quota response too large")
	}
	var payload struct {
		Plan    string     `json:"plan_type"`
		Rate    *apiLimits `json:"rate_limit"`
		Credits *struct {
			Has       bool    `json:"has_credits"`
			Unlimited bool    `json:"unlimited"`
			Balance   *string `json:"balance"`
		} `json:"credits"`
		Additional []struct {
			Name string     `json:"limit_name"`
			ID   string     `json:"metered_feature"`
			Rate *apiLimits `json:"rate_limit"`
		} `json:"additional_rate_limits"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, nil, fmt.Errorf("invalid quota response")
	}
	hasWindow := func(rate *apiLimits) bool {
		return rate != nil && (rate.Primary != nil || rate.Secondary != nil)
	}
	usable := payload.Credits != nil || hasWindow(payload.Rate)
	for _, extra := range payload.Additional {
		usable = usable || hasWindow(extra.Rate)
	}
	if !usable {
		return nil, nil, fmt.Errorf("unexpected quota response")
	}
	base := bucket{PlanType: payload.Plan}
	if payload.Rate != nil {
		base.Primary = convertWindow(payload.Rate.Primary)
		base.Secondary = convertWindow(payload.Rate.Secondary)
	}
	if payload.Credits != nil {
		base.Credits = &credits{HasCredits: payload.Credits.Has, Unlimited: payload.Credits.Unlimited}
		if payload.Credits.Balance != nil {
			base.Credits.Balance = *payload.Credits.Balance
		}
	}
	buckets := map[string]bucket{"codex": base}
	for _, extra := range payload.Additional {
		b := bucket{Name: extra.Name, PlanType: payload.Plan}
		if extra.Rate != nil {
			b.Primary = convertWindow(extra.Rate.Primary)
			b.Secondary = convertWindow(extra.Rate.Secondary)
		}
		buckets[extra.ID] = b
	}
	result := struct {
		Limits  bucket            `json:"rateLimits"`
		Buckets map[string]bucket `json:"rateLimitsByLimitId"`
	}{base, buckets}
	normalized, err := json.Marshal(result)
	return data, normalized, err
}
