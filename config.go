package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Defaults for the plugin settings. They match the defaults in
// holyarrs-plugin.json, so a plugin started with no saved settings behaves
// the same as one that has just been installed.
const (
	defaultEvery    = 60 // seconds between checks of Needs a decision
	minEvery        = 30
	maxEvery        = 3600
	defaultMaxTries = 3
	maxMaxTries     = 20

	// defaultNeedle is the part of HolyArrs' own refusal message that marks a
	// file turned away for its audio languages. The full message is
	// "inspected audio does not establish every language required by this
	// Version; ...". It is a setting so a reworded message can be followed
	// without a new release.
	defaultNeedle = "does not establish every language required"
)

// Config is everything the plugin reads from its environment.
type Config struct {
	Target   string        // HolyArrs' local address, host:port
	Token    string        // bearer key, new on every start
	DataDir  string        // the plugin's own folder
	DryRun   bool          // only alert, never turn anything down
	Every    time.Duration // time between checks
	MaxTries int           // files to try per movie or episode before leaving it to the owner
	Needle   string        // lower case text that marks an audio-language refusal
	Alerts   bool          // send a message through the owner's alerts
}

// loadConfig reads the settings HolyArrs hands to a plugin. A setting that is
// out of range is moved to the nearest allowed value and reported in notes,
// because a plugin that exits turns itself off until the owner restarts it.
func loadConfig(env func(string) string) (cfg Config, notes []string, err error) {
	cfg.Target = strings.TrimSpace(env("HOLYARRS_PLUGIN_TARGET"))
	cfg.Token = strings.TrimSpace(env("HOLYARRS_PLUGIN_TOKEN"))
	cfg.DataDir = strings.TrimSpace(env("HOLYARRS_PLUGIN_DATA"))
	if cfg.Target == "" || cfg.Token == "" {
		return cfg, nil, fmt.Errorf("HOLYARRS_PLUGIN_TARGET and HOLYARRS_PLUGIN_TOKEN are not set; this plugin needs HolyArrs 1.3.0 or newer")
	}
	if cfg.DataDir == "" {
		return cfg, nil, fmt.Errorf("HOLYARRS_PLUGIN_DATA is not set")
	}

	setting := func(key string) string {
		return strings.TrimSpace(env("HOLYARRS_PLUGIN_SETTING_" + strings.ToUpper(key)))
	}

	if cfg.DryRun, err = toggle(setting("dry_run"), true); err != nil {
		return cfg, nil, fmt.Errorf("setting dry_run: %w", err)
	}
	if cfg.Alerts, err = toggle(setting("alerts"), true); err != nil {
		return cfg, nil, fmt.Errorf("setting alerts: %w", err)
	}

	every, note, err := number(setting("check_every"), defaultEvery, minEvery, maxEvery)
	if err != nil {
		return cfg, nil, fmt.Errorf("setting check_every: %w", err)
	}
	if note != "" {
		notes = append(notes, "check_every "+note)
	}
	cfg.Every = time.Duration(every) * time.Second

	if cfg.MaxTries, note, err = number(setting("max_tries"), defaultMaxTries, 1, maxMaxTries); err != nil {
		return cfg, nil, fmt.Errorf("setting max_tries: %w", err)
	}
	if note != "" {
		notes = append(notes, "max_tries "+note)
	}

	cfg.Needle = strings.ToLower(setting("refusal_text"))
	if cfg.Needle == "" {
		cfg.Needle = defaultNeedle
	}
	return cfg, notes, nil
}

// toggle reads a toggle setting, which arrives as "true" or "false".
func toggle(s string, def bool) (bool, error) {
	switch strings.ToLower(s) {
	case "":
		return def, nil
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return def, fmt.Errorf("%q is not true or false", s)
}

// number reads a number setting, which arrives as text, and keeps it between
// lo and hi. note says when the value had to be moved.
func number(s string, def, lo, hi int) (n int, note string, err error) {
	if s == "" {
		return def, "", nil
	}
	n, err = strconv.Atoi(s)
	if err != nil {
		return def, "", fmt.Errorf("%q is not a whole number", s)
	}
	switch {
	case n < lo:
		return lo, fmt.Sprintf("was %d, raised to the lowest allowed value %d", n, lo), nil
	case n > hi:
		return hi, fmt.Sprintf("was %d, lowered to the highest allowed value %d", n, hi), nil
	}
	return n, "", nil
}
