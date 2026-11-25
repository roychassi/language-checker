package main

import (
	"strings"
	"testing"
	"time"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func baseEnv() map[string]string {
	return map[string]string{
		"HOLYARRS_PLUGIN_TARGET": "127.0.0.1:6262",
		"HOLYARRS_PLUGIN_TOKEN":  "key",
		"HOLYARRS_PLUGIN_DATA":   "/data",
	}
}

func TestConfigDefaults(t *testing.T) {
	cfg, notes, err := loadConfig(envOf(baseEnv()))
	if err != nil || len(notes) != 0 {
		t.Fatalf("err=%v notes=%v", err, notes)
	}
	want := Config{Target: "127.0.0.1:6262", Token: "key", DataDir: "/data", DryRun: true, Every: time.Minute, MaxTries: 3, Needle: defaultNeedle, Alerts: true}
	if cfg != want {
		t.Fatalf("cfg = %+v, want %+v", cfg, want)
	}
}

func TestConfigReadsSettings(t *testing.T) {
	env := baseEnv()
	env["HOLYARRS_PLUGIN_SETTING_DRY_RUN"] = "false"
	env["HOLYARRS_PLUGIN_SETTING_ALERTS"] = "false"
	env["HOLYARRS_PLUGIN_SETTING_CHECK_EVERY"] = " 120 "
	env["HOLYARRS_PLUGIN_SETTING_MAX_TRIES"] = "5"
	env["HOLYARRS_PLUGIN_SETTING_REFUSAL_TEXT"] = "Wrong Audio"
	cfg, _, err := loadConfig(envOf(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DryRun || cfg.Alerts || cfg.Every != 2*time.Minute || cfg.MaxTries != 5 || cfg.Needle != "wrong audio" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestConfigMovesOutOfRangeNumbers(t *testing.T) {
	env := baseEnv()
	env["HOLYARRS_PLUGIN_SETTING_CHECK_EVERY"] = "1"
	env["HOLYARRS_PLUGIN_SETTING_MAX_TRIES"] = "500"
	cfg, notes, err := loadConfig(envOf(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Every != minEvery*time.Second || cfg.MaxTries != maxMaxTries || len(notes) != 2 {
		t.Fatalf("cfg = %+v notes = %v", cfg, notes)
	}
}

func TestConfigRejectsWhatItCannotUse(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]string)
		want   string
	}{
		{"no key", func(m map[string]string) { delete(m, "HOLYARRS_PLUGIN_TOKEN") }, "1.3.0"},
		{"no data folder", func(m map[string]string) { delete(m, "HOLYARRS_PLUGIN_DATA") }, "DATA"},
		{"bad toggle", func(m map[string]string) { m["HOLYARRS_PLUGIN_SETTING_DRY_RUN"] = "maybe" }, "dry_run"},
		{"bad number", func(m map[string]string) { m["HOLYARRS_PLUGIN_SETTING_MAX_TRIES"] = "three" }, "max_tries"},
	}
	for _, tc := range tests {
		env := baseEnv()
		tc.mutate(env)
		if _, _, err := loadConfig(envOf(env)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want one mentioning %q", tc.name, err, tc.want)
		}
	}
}
