// Language Checker is a HolyArrs plugin. HolyArrs reads the real audio tracks
// of a download when it imports it, and refuses a file that lacks the audio
// languages the Version asks for; the file then waits in Needs a decision.
// This plugin watches for those refusals and turns the refused release down,
// so HolyArrs blocklists it for that episode and searches again, up to a set
// number of files per episode.
//
// It reads no media, deletes nothing, and needs no outside network access.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

const version = "1.0.0"

func main() {
	// HolyArrs puts "plugin <id>:" in front of every line and adds the time.
	log.SetFlags(0)
	if err := run(); err != nil {
		log.Printf("stopping: %v", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, notes, err := loadConfig(os.Getenv)
	if err != nil {
		return err
	}
	for _, n := range notes {
		log.Printf("setting %s", n)
	}

	store, note, err := OpenStore(filepath.Join(cfg.DataDir, "state.json"))
	if err != nil {
		return fmt.Errorf("opening saved state: %w", err)
	}
	if note != "" {
		log.Print(note)
	}

	mode := "turning down releases"
	if cfg.DryRun {
		mode = "dry run, alerts only"
	}
	log.Printf("language-checker %s started: %s, checking every %s, up to %d files per episode", version, mode, cfg.Every, cfg.MaxTries)

	// HolyArrs stops a plugin with SIGTERM; finish the current call and leave.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	engine := NewEngine(cfg, NewClient(cfg.Target, cfg.Token), store, log.Printf)
	err = engine.Run(ctx)
	// One last save in case a stop arrived between a change and the next write.
	if serr := store.Save(engine.now()); serr != nil {
		log.Printf("could not save state: %v", serr)
	}
	log.Print("stopped")
	return err
}
