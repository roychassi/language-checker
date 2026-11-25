package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// alertSubject heads every message. HolyArrs allows up to 100 characters.
	alertSubject = "Language Checker"

	// alertsPerHour stays under the 20 messages an hour HolyArrs lets one
	// plugin send, so a busy hour never ends in a refused message.
	alertsPerHour = 15

	// maxAlertText is HolyArrs' limit for the body of one message.
	maxAlertText = 1000

	// maxQueued bounds the lines waiting to be sent if alerts keep failing.
	maxQueued = 50

	maxBackoff = 5 * time.Minute
)

// Engine finds the files HolyArrs turned away for their audio languages and
// turns their releases down, so HolyArrs searches again.
//
// It never reads media files and never deletes anything. HolyArrs reads the
// real audio tracks when a download is imported; when the Version asks for a
// language the tracks don't show, the file is refused before it enters the
// library and waits in Needs a decision. That is the only case acted on here.
type Engine struct {
	cfg   Config
	api   API
	store *Store
	now   func() time.Time
	logf  func(format string, args ...any)

	queue []string    // alert lines waiting to be sent
	sent  []time.Time // when alerts went out, for the hourly cap
}

// NewEngine wires the engine to HolyArrs and to its saved state.
func NewEngine(cfg Config, api API, store *Store, logf func(string, ...any)) *Engine {
	return &Engine{cfg: cfg, api: api, store: store, now: time.Now, logf: logf}
}

// Run checks until ctx ends. A failed check is retried with a growing pause,
// so a HolyArrs that is restarting is not hammered.
func (e *Engine) Run(ctx context.Context) error {
	fails := 0
	for {
		wait := e.cfg.Every
		if err := e.Cycle(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			fails++
			wait = backoff(e.cfg.Every, fails, err)
			e.logf("check failed (%d in a row), trying again in %s: %v", fails, wait, err)
		} else {
			fails = 0
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
	}
}

// backoff doubles the pause after each failure up to maxBackoff, or waits as
// long as HolyArrs asked when it said to slow down.
func backoff(base time.Duration, fails int, err error) time.Duration {
	wait := base
	for i := 1; i < fails && wait < maxBackoff; i++ {
		wait *= 2
	}
	if wait > maxBackoff {
		wait = maxBackoff
	}
	var ae *APIError
	if errors.As(err, &ae) && ae.RetryAfter > wait {
		wait = ae.RetryAfter
	}
	return wait
}

// Cycle makes one pass over Needs a decision.
func (e *Engine) Cycle(ctx context.Context) error {
	items, err := e.api.Decisions(ctx)
	if err != nil {
		return err
	}
	var first error
	for _, d := range items {
		if ctx.Err() != nil {
			break
		}
		if !strings.Contains(strings.ToLower(d.Reason), e.cfg.Needle) {
			continue
		}
		if err := e.handle(ctx, d); err != nil {
			e.logf("%v", err)
			if first == nil {
				first = err
			}
		}
	}
	if err := e.store.Save(e.now()); err != nil {
		e.logf("could not save state: %v", err)
	}
	e.flush(ctx)
	return first
}

// handle deals with one item that was turned away for its audio languages.
func (e *Engine) handle(ctx context.Context, d Decision) error {
	if d.RetryAt != nil || d.ReleaseID == nil {
		// HolyArrs will try the import again by itself, or there is no
		// release to turn down. Either way there is nothing for us to do.
		return nil
	}
	now := e.now()
	name := describe(d)
	rel := *d.ReleaseID
	ep := e.store.Get(d.SlotID, name, now)

	if e.cfg.DryRun {
		if !containsID(ep.Seen, rel) {
			ep.Seen = append(ep.Seen, rel)
			if len(ep.Seen) > maxSeen {
				ep.Seen = ep.Seen[len(ep.Seen)-maxSeen:]
			}
			ep.Updated = now
			e.say("Dry run: %s for %s has no English audio. It would be turned down.", releaseText(d), name)
		}
		return nil
	}

	// Count each refused release once, the first time it is seen. A new
	// release after the limit was reached means the owner searched again by
	// hand, which starts a fresh round.
	if rel != ep.Last {
		if ep.GaveUp {
			ep.Tries, ep.GaveUp = 0, false
		}
		ep.Last = rel
		ep.Tries++
		ep.Updated = now
	}
	if ep.Tries >= e.cfg.MaxTries {
		// The limit counts files tried. The last one stays in Needs a
		// decision, where the owner can search again by hand, or raise the
		// limit and let the plugin carry on.
		if !ep.GaveUp {
			ep.GaveUp = true
			ep.Updated = now
			e.say("%s: %d files tried and none had English audio. The last one is waiting in Needs a decision. Search again there for a fresh round, or raise Max tries to let me carry on with this one.", name, ep.Tries)
		}
		return nil
	}
	ep.GaveUp = false
	if ep.Down == rel {
		return nil // already turned down; HolyArrs has not moved the slot on yet
	}

	err := e.api.TurnDown(ctx, d.SlotID)
	switch {
	case moved(err):
		return nil // someone else decided first
	case err != nil:
		return fmt.Errorf("turning down %s for %s: %w", releaseText(d), name, err)
	}
	ep.Down = rel
	ep.Updated = now
	e.logf("turned down %s for %s: no English audio (file %d of %d)", releaseText(d), name, ep.Tries, e.cfg.MaxTries)
	e.say("Turned down %s for %s: no English audio in the file. Searching again (file %d of %d).", releaseText(d), name, ep.Tries, e.cfg.MaxTries)
	return nil
}

// say queues one line for the owner's alerts.
func (e *Engine) say(format string, args ...any) {
	if !e.cfg.Alerts {
		return
	}
	e.queue = append(e.queue, clean(fmt.Sprintf(format, args...)))
	if len(e.queue) > maxQueued {
		e.queue = e.queue[len(e.queue)-maxQueued:]
	}
}

// flush sends the queued lines as one message, as many as fit in one, and
// keeps the rest for the next check. A message that fails stays queued.
func (e *Engine) flush(ctx context.Context) {
	if len(e.queue) == 0 || ctx.Err() != nil {
		return
	}
	now := e.now()
	recent := e.sent[:0]
	for _, t := range e.sent {
		if now.Sub(t) < time.Hour {
			recent = append(recent, t)
		}
	}
	e.sent = recent
	if len(e.sent) >= alertsPerHour {
		return
	}

	n, size := 0, 0
	for _, line := range e.queue {
		add := utf8.RuneCountInString(line) + 1
		if n > 0 && size+add > maxAlertText {
			break
		}
		n++
		size += add
	}
	text := truncate(strings.Join(e.queue[:n], "\n"), maxAlertText)
	if err := e.api.Notify(ctx, alertSubject, text); err != nil {
		e.logf("could not send the alert, will try again: %v", err)
		return
	}
	e.sent = append(e.sent, now)
	e.queue = append([]string(nil), e.queue[n:]...)
}

// describe names an item the way the owner sees it: "Black Butler S01E20" for
// an episode, "Title (2008)" for a movie.
func describe(d Decision) string {
	switch {
	case d.Episode != nil && d.Episode.Label != "":
		return clean(d.Title + " " + d.Episode.Label)
	case d.Year != nil:
		return clean(fmt.Sprintf("%s (%d)", d.Title, *d.Year))
	}
	return clean(d.Title)
}

func releaseText(d Decision) string {
	if d.ReleaseName == "" {
		return "the release"
	}
	return truncate(clean(d.ReleaseName), 90)
}

// clean takes out what HolyArrs refuses in a message: markup characters and
// control characters.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '<' || r == '>' || r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

// truncate cuts s to at most n characters, ending in an ellipsis when it cut.
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

func containsID(ids []int64, id int64) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}
