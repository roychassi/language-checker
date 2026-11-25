package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientTalksToHolyArrs(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret-key" {
			t.Errorf("Authorization = %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Method+" "+r.URL.Path+" "+strings.TrimSpace(string(body)))
		switch r.URL.Path {
		case "/api/v1/needs-decision":
			io.WriteString(w, `{"count":1,"items":[{"slot_id":7,"title":"Black Butler","kind":"show","reason":"r","release_id":42,"release_name":"Rel","episode":{"label":"S01E20"},"unknown_field":true}]}`)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()
	c := NewClient(strings.TrimPrefix(srv.URL, "http://"), "secret-key")
	ctx := context.Background()

	items, err := c.Decisions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].SlotID != 7 || *items[0].ReleaseID != 42 || items[0].Episode.Label != "S01E20" {
		t.Fatalf("items = %+v", items)
	}
	if err := c.TurnDown(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if err := c.Notify(ctx, "Subject", "Text"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"GET /api/v1/needs-decision ",
		`POST /api/v1/needs-decision/7/decide {"action":"turn_down"}`,
		`POST /api/v1/plugin-hooks/notify {"subject":"Subject","text":"Text"}`,
	}
	if len(seen) != len(want) {
		t.Fatalf("requests = %q", seen)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Errorf("request %d = %q, want %q", i, seen[i], want[i])
		}
	}
}

func TestClientAcceptsAFullURLTarget(t *testing.T) {
	if got := NewClient("http://127.0.0.1:6262/", "k").base; got != "http://127.0.0.1:6262" {
		t.Fatalf("base = %q", got)
	}
	if got := NewClient("127.0.0.1:6262", "k").base; got != "http://127.0.0.1:6262" {
		t.Fatalf("base = %q", got)
	}
}

func TestClientErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/needs-decision":
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]string{"error": "this plugin is calling too often"})
		case "/api/v1/needs-decision/9/decide":
			w.WriteHeader(http.StatusConflict)
			io.WriteString(w, `{"error":"the slot no longer needs a decision"}`)
		default:
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, "not json")
		}
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "k")
	ctx := context.Background()

	_, err := c.Decisions(ctx)
	ae, ok := err.(*APIError)
	if !ok || ae.Status != 429 || ae.RetryAfter != 30*time.Second || !strings.Contains(ae.Error(), "calling too often") {
		t.Fatalf("err = %#v", err)
	}
	if err := c.TurnDown(ctx, 9); !moved(err) {
		t.Fatalf("a 409 should count as moved on, got %v", err)
	}
	err = c.Notify(ctx, "s", "t")
	if ae, ok := err.(*APIError); !ok || ae.Status != 403 || moved(err) {
		t.Fatalf("err = %#v", err)
	}
}

func TestClientStopsWhenTheContextEnds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := NewClient(srv.URL, "k").Decisions(ctx); err == nil {
		t.Fatal("want an error once the context ended")
	}
}
