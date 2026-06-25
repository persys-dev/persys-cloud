package gitops

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/fsnotify/fsnotify"
)

// WatchLocal watches a local directory and calls apply when files change.
func WatchLocal(ctx context.Context, opts WatchOptions, apply func(context.Context, Event) error) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	defer w.Close()
	if err := w.Add(opts.Path); err != nil {
		return fmt.Errorf("watch %q: %w", opts.Path, err)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-w.Errors:
			if err != nil {
				return fmt.Errorf("watch error: %w", err)
			}
		case ev := <-w.Events:
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
				if err := apply(ctx, Event{Path: ev.Name, Time: time.Now()}); err != nil {
					return err
				}
			}
		}
	}
}

// WatchRemote periodically runs git pull in Path and calls apply after successful updates.
func WatchRemote(ctx context.Context, opts WatchOptions, apply func(context.Context, Event) error) error {
	interval := opts.Interval
	if interval <= 0 {
		interval = time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			out, err := exec.CommandContext(ctx, "git", "-C", opts.Path, "pull", "--ff-only").CombinedOutput()
			if err != nil {
				return fmt.Errorf("git pull: %w: %s", err, out)
			}
			if err := apply(ctx, Event{Path: opts.Path, Time: time.Now()}); err != nil {
				return err
			}
		}
	}
}
