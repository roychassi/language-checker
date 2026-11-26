package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Decision is one movie, episode or track waiting in Needs a decision. Only
// the fields this plugin uses are listed.
type Decision struct {
	SlotID      int64      `json:"slot_id"`
	Title       string     `json:"title"`
	Year        *int       `json:"year,omitempty"`
	Version     string     `json:"version"`
	Kind        string     `json:"kind"` // movie, show or artist
	Reason      string     `json:"reason"`
	ReleaseID   *int64     `json:"release_id,omitempty"`
	ReleaseName string     `json:"release_name,omitempty"`
	RetryAt     *time.Time `json:"retry_at,omitempty"` // set when HolyArrs will try the import again by itself
	Episode     *struct {
		Label string `json:"label"` // such as S01E20
	} `json:"episode,omitempty"`
}

// API is the part of HolyArrs this plugin talks to. The engine depends on this
// interface, not on Client, so the tests can stand in for HolyArrs.
type API interface {
	Decisions(ctx context.Context) ([]Decision, error)
	TurnDown(ctx context.Context, slotID int64) error
	Notify(ctx context.Context, subject, text string) error
}

// APIError is an answer from HolyArrs that was not a success.
type APIError struct {
	Status     int
	Message    string
	RetryAfter time.Duration // from a 429 answer, else zero
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("HolyArrs answered %d", e.Status)
	}
	return fmt.Sprintf("HolyArrs answered %d: %s", e.Status, e.Message)
}

// moved reports whether err means the item no longer waits for a decision,
// which is not a failure: the owner or HolyArrs got there first.
func moved(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && (ae.Status == http.StatusNotFound || ae.Status == http.StatusConflict)
}

// Client calls HolyArrs with the plugin's own key. The key is only ever sent
// in the Authorization header and is never logged.
type Client struct {
	base  string
	token string
	http  *http.Client
}

// NewClient takes the address HolyArrs gives a plugin (host:port).
func NewClient(target, token string) *Client {
	base := strings.TrimRight(target, "/")
	if !strings.Contains(base, "://") {
		base = "http://" + base
	}
	return &Client{base: base, token: token, http: &http.Client{Timeout: 20 * time.Second}}
}

// Decisions lists what waits in Needs a decision.
func (c *Client) Decisions(ctx context.Context) ([]Decision, error) {
	var out struct {
		Items []Decision `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/needs-decision", nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// TurnDown turns down the release behind a slot, for that movie or episode, and
// makes HolyArrs search again. HolyArrs does both in this one call.
func (c *Client) TurnDown(ctx context.Context, slotID int64) error {
	path := "/api/v1/needs-decision/" + strconv.FormatInt(slotID, 10) + "/decide"
	return c.do(ctx, http.MethodPost, path, map[string]any{"action": "turn_down"}, nil)
}

// Notify sends one message through the owner's alerts that take plugin
// messages.
func (c *Client) Notify(ctx context.Context, subject, text string) error {
	return c.do(ctx, http.MethodPost, "/api/v1/plugin-hooks/notify", map[string]string{"subject": subject, "text": text}, nil)
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// A list of Needs a decision is small; the cap only stops a runaway answer.
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return apiError(resp, data)
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("reading the answer from %s: %w", path, err)
	}
	return nil
}

// apiError builds the error for a failed answer. HolyArrs sends {"error": "..."}.
func apiError(resp *http.Response, data []byte) *APIError {
	e := &APIError{Status: resp.StatusCode}
	var msg struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &msg) == nil {
		e.Message = msg.Error
	}
	if len(e.Message) > 200 {
		e.Message = e.Message[:200]
	}
	if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
		e.RetryAfter = time.Duration(s) * time.Second
	}
	return e
}
