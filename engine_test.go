package main

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

const audioRefusal = "inspected audio does not establish every language required by this Version; choose a suitable copy or review the requirement"

// fakeAPI stands in for HolyArrs.
type fakeAPI struct {
	items   []Decision
	listErr error
	downErr map[int64]error
	noteErr error

	downs []int64  // slots turned down, in order
	notes []string // texts of the messages sent
}

func (f *fakeAPI) Decisions(context.Context) ([]Decision, error) { return f.items, f.listErr }

func (f *fakeAPI) TurnDown(_ context.Context, slot int64) error {
	if err := f.downErr[slot]; err != nil {
		return err
	}
	f.downs = append(f.downs, slot)
	return nil
}

func (f *fakeAPI) Notify(_ context.Context, subject, text string) error {
	if f.noteErr != nil {
		return f.noteErr
	}
	if subject != alertSubject {
		panic("unexpected subject " + subject)
	}
	f.notes = append(f.notes, text)
	return nil
}

func episode(slot, release int64, reason string) Decision {
	id := release
	return Decision{
		SlotID: slot, Title: "Black Butler", Kind: "show", Reason: reason,
		ReleaseID: &id, ReleaseName: "Black.Butler.2008.S01E20.MULTI.1080p.WEB-DL.H264-2B1C",
		Episode: &struct {
			Label string `json:"label"`
		}{Label: "S01E20"},
	}
}

func newTestEngine(t *testing.T, api *fakeAPI, mutate func(*Config)) *Engine {
	t.Helper()
	cfg := Config{DryRun: false, Every: time.Minute, MaxTries: 3, Needle: defaultNeedle, Alerts: true}
	if mutate != nil {
		mutate(&cfg)
	}
	store, _, err := OpenStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(cfg, api, store, func(string, ...any) {})
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return now }
	return e
}

func cycle(t *testing.T, e *Engine) {
	t.Helper()
	if err := e.Cycle(context.Background()); err != nil {
		t.Fatalf("cycle: %v", err)
	}
}

func TestIgnoresOtherDecisions(t *testing.T) {
	api := &fakeAPI{items: []Decision{
		episode(1, 10, "nothing in the download could be matched"),
		episode(2, 11, "no file in the download matched"),
	}}
	e := newTestEngine(t, api, nil)
	cycle(t, e)
	if len(api.downs) != 0 || len(api.notes) != 0 {
		t.Fatalf("acted on a decision that was not an audio refusal: downs=%v notes=%v", api.downs, api.notes)
	}
}

func TestRefusalMatchIgnoresCase(t *testing.T) {
	api := &fakeAPI{items: []Decision{episode(1, 10, strings.ToUpper(audioRefusal))}}
	e := newTestEngine(t, api, nil)
	cycle(t, e)
	if len(api.downs) != 1 {
		t.Fatalf("downs = %v, want one", api.downs)
	}
}

func TestCustomRefusalText(t *testing.T) {
	api := &fakeAPI{items: []Decision{episode(1, 10, "audio is wrong for this version")}}
	e := newTestEngine(t, api, func(c *Config) { c.Needle = "audio is wrong" })
	cycle(t, e)
	if len(api.downs) != 1 {
		t.Fatalf("downs = %v, want one", api.downs)
	}
}

func TestDryRunReportsOnceAndChangesNothing(t *testing.T) {
	api := &fakeAPI{items: []Decision{episode(1, 10, audioRefusal)}}
	e := newTestEngine(t, api, func(c *Config) { c.DryRun = true })
	cycle(t, e)
	cycle(t, e)
	if len(api.downs) != 0 {
		t.Fatalf("dry run turned something down: %v", api.downs)
	}
	if len(api.notes) != 1 || !strings.Contains(api.notes[0], "Dry run") || !strings.Contains(api.notes[0], "Black Butler S01E20") {
		t.Fatalf("notes = %q, want one dry run message naming the episode", api.notes)
	}
	if strings.Contains(strings.ToLower(api.notes[0]), "english") {
		t.Fatalf("message = %q, the plugin works for any required language", api.notes[0])
	}
	if got := e.store.Episodes[1].Tries; got != 0 {
		t.Fatalf("dry run counted %d tries, want 0", got)
	}
}

func TestTurnsDownAndCounts(t *testing.T) {
	api := &fakeAPI{items: []Decision{episode(5, 11, audioRefusal)}}
	e := newTestEngine(t, api, nil)
	cycle(t, e)
	if len(api.downs) != 1 || api.downs[0] != 5 {
		t.Fatalf("downs = %v, want slot 5 once", api.downs)
	}
	if len(api.notes) != 1 || !strings.Contains(api.notes[0], "file 1 of 3") {
		t.Fatalf("notes = %q, want a message saying file 1 of 3", api.notes)
	}
	// The refusal fires for any language a Version requires, so the text must not name one.
	if !strings.Contains(api.notes[0], "required audio language") || strings.Contains(strings.ToLower(api.notes[0]), "english") {
		t.Fatalf("message = %q, want it to speak of the required audio language, not English", api.notes[0])
	}
}

func TestSameReleaseStillListedIsNotTurnedDownTwice(t *testing.T) {
	api := &fakeAPI{items: []Decision{episode(5, 11, audioRefusal)}}
	e := newTestEngine(t, api, nil)
	cycle(t, e)
	cycle(t, e)
	cycle(t, e)
	if len(api.downs) != 1 {
		t.Fatalf("turned the same release down %d times", len(api.downs))
	}
	if got := e.store.Episodes[5].Tries; got != 1 {
		t.Fatalf("tries = %d, want 1", got)
	}
}

// The limit counts files tried: the last one is left for the owner.
func TestLeavesTheLastFileAtTheLimitAndSaysSoOnce(t *testing.T) {
	api := &fakeAPI{}
	e := newTestEngine(t, api, nil)
	for _, rel := range []int64{11, 12, 13} {
		api.items = []Decision{episode(5, rel, audioRefusal)}
		cycle(t, e)
	}
	if want := []int64{5, 5}; len(api.downs) != 2 {
		t.Fatalf("downs = %v, want %v (the third file stays)", api.downs, want)
	}
	last := api.notes[len(api.notes)-1]
	if !strings.Contains(last, "3 files tried") || !strings.Contains(last, "Needs a decision") || strings.Contains(strings.ToLower(last), "english") {
		t.Fatalf("last message = %q, want the limit message", last)
	}
	before := len(api.notes)
	cycle(t, e)
	cycle(t, e)
	if len(api.notes) != before || len(api.downs) != 2 {
		t.Fatalf("kept acting after the limit: notes %d->%d downs=%v", before, len(api.notes), api.downs)
	}
}

func TestRaisingTheLimitCarriesOn(t *testing.T) {
	api := &fakeAPI{}
	e := newTestEngine(t, api, nil)
	for _, rel := range []int64{11, 12, 13} {
		api.items = []Decision{episode(5, rel, audioRefusal)}
		cycle(t, e)
	}
	e.cfg.MaxTries = 5 // what a restart with a new setting does
	cycle(t, e)
	if len(api.downs) != 3 {
		t.Fatalf("downs = %v, want the waiting file turned down after raising the limit", api.downs)
	}
}

func TestSearchingAgainByHandStartsAFreshRound(t *testing.T) {
	api := &fakeAPI{}
	e := newTestEngine(t, api, nil)
	for _, rel := range []int64{11, 12, 13} {
		api.items = []Decision{episode(5, rel, audioRefusal)}
		cycle(t, e)
	}
	api.items = []Decision{episode(5, 14, audioRefusal)} // the owner pressed search and got a new file
	cycle(t, e)
	if len(api.downs) != 3 {
		t.Fatalf("downs = %v, want the new file turned down", api.downs)
	}
	if got := e.store.Episodes[5].Tries; got != 1 {
		t.Fatalf("tries = %d, want a fresh count of 1", got)
	}
}

func TestSkipsWhatHolyArrsHandlesOrCannotTurnDown(t *testing.T) {
	retry := time.Now().Add(time.Hour)
	waiting := episode(1, 10, audioRefusal)
	waiting.RetryAt = &retry
	noRelease := episode(2, 0, audioRefusal)
	noRelease.ReleaseID = nil
	api := &fakeAPI{items: []Decision{waiting, noRelease}}
	e := newTestEngine(t, api, nil)
	cycle(t, e)
	if len(api.downs) != 0 || len(api.notes) != 0 {
		t.Fatalf("acted on items it should skip: downs=%v notes=%v", api.downs, api.notes)
	}
}

func TestSomeoneElseDecidedFirstIsNotAnError(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusConflict} {
		api := &fakeAPI{
			items:   []Decision{episode(5, 11, audioRefusal)},
			downErr: map[int64]error{5: &APIError{Status: status}},
		}
		e := newTestEngine(t, api, nil)
		if err := e.Cycle(context.Background()); err != nil {
			t.Fatalf("status %d: %v", status, err)
		}
		if len(api.notes) != 0 {
			t.Fatalf("status %d: told the owner about a turn down that did not happen: %q", status, api.notes)
		}
	}
}

func TestOneFailureDoesNotStopTheOthers(t *testing.T) {
	api := &fakeAPI{
		items:   []Decision{episode(1, 10, audioRefusal), episode(2, 20, audioRefusal)},
		downErr: map[int64]error{1: &APIError{Status: http.StatusInternalServerError, Message: "boom"}},
	}
	e := newTestEngine(t, api, nil)
	err := e.Cycle(context.Background())
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want the failure reported", err)
	}
	if len(api.downs) != 1 || api.downs[0] != 2 {
		t.Fatalf("downs = %v, want slot 2 turned down despite slot 1 failing", api.downs)
	}
}

func TestListFailureIsReturned(t *testing.T) {
	want := errors.New("down")
	e := newTestEngine(t, &fakeAPI{listErr: want}, nil)
	if err := e.Cycle(context.Background()); !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestAlertsCanBeSwitchedOff(t *testing.T) {
	api := &fakeAPI{items: []Decision{episode(5, 11, audioRefusal)}}
	e := newTestEngine(t, api, func(c *Config) { c.Alerts = false })
	cycle(t, e)
	if len(api.downs) != 1 || len(api.notes) != 0 {
		t.Fatalf("downs=%v notes=%v, want a turn down and no message", api.downs, api.notes)
	}
}

func TestFailedAlertIsKeptAndRetried(t *testing.T) {
	api := &fakeAPI{items: []Decision{episode(5, 11, audioRefusal)}, noteErr: errors.New("alerts down")}
	e := newTestEngine(t, api, nil)
	cycle(t, e)
	if len(e.queue) != 1 {
		t.Fatalf("queue = %d lines, want the message kept", len(e.queue))
	}
	api.noteErr = nil
	cycle(t, e)
	if len(api.notes) != 1 || len(e.queue) != 0 {
		t.Fatalf("notes=%d queue=%d, want the message delivered once", len(api.notes), len(e.queue))
	}
}

func TestStaysUnderTheHourlyAlertCap(t *testing.T) {
	api := &fakeAPI{items: []Decision{episode(5, 11, audioRefusal)}}
	e := newTestEngine(t, api, nil)
	for i := 0; i < alertsPerHour; i++ {
		e.sent = append(e.sent, e.now().Add(-time.Duration(i)*time.Minute))
	}
	cycle(t, e)
	if len(api.notes) != 0 {
		t.Fatal("sent a message over the hourly cap")
	}
	e.now = func() time.Time { return time.Date(2026, 11, 24, 14, 0, 0, 0, time.UTC) }
	cycle(t, e)
	if len(api.notes) != 1 {
		t.Fatalf("notes = %d, want the queued message sent once the hour passed", len(api.notes))
	}
}

func TestAlertTextFitsAndHasNoMarkup(t *testing.T) {
	api := &fakeAPI{}
	for i := int64(1); i <= 30; i++ {
		d := episode(i, i+100, audioRefusal)
		d.Title = "Show <b>" + strings.Repeat("x", 40) + "</b>"
		api.items = append(api.items, d)
	}
	e := newTestEngine(t, api, nil)
	cycle(t, e)
	if len(api.notes) != 1 {
		t.Fatalf("notes = %d, want one message per check", len(api.notes))
	}
	text := api.notes[0]
	if utf8.RuneCountInString(text) > maxAlertText {
		t.Fatalf("message is %d characters, HolyArrs allows %d", utf8.RuneCountInString(text), maxAlertText)
	}
	if strings.ContainsAny(text, "<>") {
		t.Fatalf("message has markup characters: %q", text)
	}
	if len(e.queue) == 0 {
		t.Fatal("lines that did not fit were dropped instead of kept for the next check")
	}
	cycle(t, e)
	if len(api.notes) != 2 {
		t.Fatalf("notes = %d, want the rest sent on the next check", len(api.notes))
	}
}

func TestMemorySurvivesARestart(t *testing.T) {
	api := &fakeAPI{items: []Decision{episode(5, 11, audioRefusal)}}
	e := newTestEngine(t, api, nil)
	cycle(t, e)

	store, _, err := OpenStore(e.store.path)
	if err != nil {
		t.Fatal(err)
	}
	e2 := NewEngine(e.cfg, api, store, func(string, ...any) {})
	e2.now = e.now
	cycle(t, e2) // the same release is still listed after the restart
	if len(api.downs) != 1 {
		t.Fatalf("downs = %v, want no second turn down after a restart", api.downs)
	}
	api.items = []Decision{episode(5, 12, audioRefusal)}
	cycle(t, e2)
	if got := e2.store.Episodes[5].Tries; got != 2 {
		t.Fatalf("tries = %d, want the count to carry over to 2", got)
	}
}

func TestDescribe(t *testing.T) {
	year := 2008
	movie := Decision{Title: "Kuroshitsuji", Year: &year}
	tests := []struct {
		name string
		d    Decision
		want string
	}{
		{"episode", episode(1, 1, ""), "Black Butler S01E20"},
		{"movie", movie, "Kuroshitsuji (2008)"},
		{"bare", Decision{Title: "Thing"}, "Thing"},
	}
	for _, tc := range tests {
		if got := describe(tc.d); got != tc.want {
			t.Errorf("%s: describe = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestBackoff(t *testing.T) {
	base := time.Minute
	if got := backoff(base, 1, errors.New("x")); got != base {
		t.Errorf("first failure waits %s, want %s", got, base)
	}
	if got := backoff(base, 3, errors.New("x")); got != 4*base {
		t.Errorf("third failure waits %s, want %s", got, 4*base)
	}
	if got := backoff(base, 30, errors.New("x")); got != maxBackoff {
		t.Errorf("many failures wait %s, want the cap %s", got, maxBackoff)
	}
	slow := &APIError{Status: http.StatusTooManyRequests, RetryAfter: 4 * time.Minute}
	if got := backoff(base, 1, slow); got != 4*time.Minute {
		t.Errorf("a 429 with Retry-After waits %s, want 4m", got)
	}
}

// flakyAPI fails the first lists, then answers.
type flakyAPI struct {
	fakeAPI
	failures int
	lists    chan struct{}
}

func (f *flakyAPI) Decisions(ctx context.Context) ([]Decision, error) {
	f.lists <- struct{}{}
	if f.failures > 0 {
		f.failures--
		return nil, errors.New("HolyArrs is restarting")
	}
	return f.fakeAPI.Decisions(ctx)
}

func TestRunKeepsGoingAfterFailuresAndStopsWhenAsked(t *testing.T) {
	api := &flakyAPI{failures: 2, lists: make(chan struct{}, 10)}
	api.items = []Decision{episode(5, 11, audioRefusal)}
	e := newTestEngine(t, &api.fakeAPI, func(c *Config) { c.Every = time.Millisecond })
	e.api = api
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx) }()

	for i := 0; i < 4; i++ { // two failures, then the checks that follow
		select {
		case <-api.lists:
		case <-time.After(5 * time.Second):
			t.Fatalf("check %d never ran", i+1)
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v after being asked to stop", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
	if len(api.downs) != 1 {
		t.Fatalf("downs = %v, want the refusal handled once after the failures", api.downs)
	}
}

// crashAPI "dies" during the call for slot 2, as a plugin killed in the middle of a check would.
type crashAPI struct{ fakeAPI }

func (c *crashAPI) TurnDown(ctx context.Context, slot int64) error {
	if slot == 2 {
		panic("killed")
	}
	return c.fakeAPI.TurnDown(ctx, slot)
}

func TestAFinishedTurnDownIsOnDiskBeforeTheNextCallStarts(t *testing.T) {
	api := &crashAPI{fakeAPI{items: []Decision{episode(1, 10, audioRefusal), episode(2, 20, audioRefusal)}}}
	e := newTestEngine(t, &api.fakeAPI, nil)
	e.api = api
	func() {
		defer func() { recover() }()
		_ = e.Cycle(context.Background())
	}()
	store, _, err := OpenStore(e.store.path)
	if err != nil {
		t.Fatal(err)
	}
	if got := store.Episodes[1]; got == nil || got.Down != 10 || got.Tries != 1 {
		t.Fatalf("slot 1 on disk = %+v, want the finished turn down recorded before the crash", got)
	}
}
