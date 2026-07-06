// Package gitops provides continuous synchronisation between desired
// configuration and Persys cluster state.
//
// Two watcher implementations are provided:
//
//   - FSWatcher: watches a local directory for manifest changes using fsnotify.
//   - RepoWatcher: polls a remote Git repository and triggers reconciliation
//     when the tracked ref advances.
//
// Flow:
//
//	Git Repository / Local FS
//	        │
//	        ▼
//	  GitOps Watcher
//	        │
//	        ▼
//	 Manifest Ingestion
//	        │
//	        ▼
//	   SDK Client
//	        │
//	        ▼
//	  Persys API Gateway
package gitops

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/persys-dev/persysctl/internal/ingestion"
	"github.com/persys-dev/persysctl/internal/types"
)

// ReconcileFunc is called whenever the watcher detects that the desired state
// has changed. It receives the updated workload derived from the new manifest.
type ReconcileFunc func(ctx context.Context, w *types.Workload) error

// FSWatcher watches a local directory for YAML/JSON/Compose manifest changes
// and triggers reconciliation when files are created or modified.
type FSWatcher struct {
	dir       string
	reconcile ReconcileFunc
	watcher   *fsnotify.Watcher
}

// NewFSWatcher creates an FSWatcher that monitors dir and calls reconcile
// whenever a manifest file changes.
func NewFSWatcher(dir string, reconcile ReconcileFunc) (*FSWatcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("gitops: create fsnotify watcher: %w", err)
	}
	if err := w.Add(dir); err != nil {
		w.Close()
		return nil, fmt.Errorf("gitops: watch directory %q: %w", dir, err)
	}
	return &FSWatcher{dir: dir, reconcile: reconcile, watcher: w}, nil
}

// Run starts the watch loop. It blocks until ctx is cancelled.
func (fw *FSWatcher) Run(ctx context.Context) error {
	defer fw.watcher.Close()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-fw.watcher.Events:
			if !ok {
				return nil
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				if err := fw.handleEvent(ctx, event.Name); err != nil {
					// Log and continue; a single bad file should not stop the watcher.
					fmt.Fprintf(os.Stderr, "gitops: reconcile %s: %v\n", event.Name, err)
				}
			}
		case err, ok := <-fw.watcher.Errors:
			if !ok {
				return nil
			}
			fmt.Fprintf(os.Stderr, "gitops: watcher error: %v\n", err)
		}
	}
}

func (fw *FSWatcher) handleEvent(ctx context.Context, path string) error {
	ext := strings.ToLower(filepath.Ext(path))
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	var w *types.Workload
	switch ext {
	case ".yaml", ".yml":
		src := ingestion.Detect(string(data))
		if src == ingestion.SourceCompose {
			w, err = ingestion.FromCompose(data)
		} else {
			w, err = ingestion.FromYAML(data)
		}
	case ".json":
		w, err = ingestion.FromJSON(data)
	default:
		return nil // not a manifest file; skip
	}
	if err != nil {
		return fmt.Errorf("ingest %s: %w", path, err)
	}
	return fw.reconcile(ctx, w)
}

// RepoWatcher polls a remote Git repository and triggers reconciliation when
// the tracked ref advances.
type RepoWatcher struct {
	repoURL   string
	ref       string
	localPath string
	interval  time.Duration
	reconcile ReconcileFunc

	lastCommit string
}

// RepoWatcherOptions configures a RepoWatcher.
type RepoWatcherOptions struct {
	// RepoURL is the Git remote URL.
	RepoURL string
	// Ref is the branch or tag to track. Default: "main".
	Ref string
	// LocalPath is the directory used for the local clone.
	LocalPath string
	// PollInterval is how often to check for new commits. Default: 30s.
	PollInterval time.Duration
}

// NewRepoWatcher creates a RepoWatcher. It performs an initial clone or
// fetch of the repository before returning.
func NewRepoWatcher(ctx context.Context, opts RepoWatcherOptions, reconcile ReconcileFunc) (*RepoWatcher, error) {
	if opts.Ref == "" {
		opts.Ref = "main"
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 30 * time.Second
	}

	rw := &RepoWatcher{
		repoURL:   opts.RepoURL,
		ref:       opts.Ref,
		localPath: opts.LocalPath,
		interval:  opts.PollInterval,
		reconcile: reconcile,
	}

	if err := rw.ensureClone(ctx); err != nil {
		return nil, fmt.Errorf("gitops: initial clone: %w", err)
	}
	return rw, nil
}

// Run starts the polling loop. It blocks until ctx is cancelled.
func (rw *RepoWatcher) Run(ctx context.Context) error {
	ticker := time.NewTicker(rw.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			changed, err := rw.pull(ctx)
			if err != nil {
				fmt.Fprintf(os.Stderr, "gitops: pull %s: %v\n", rw.repoURL, err)
				continue
			}
			if !changed {
				continue
			}
			if err := rw.reconcileAll(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "gitops: reconcile after pull: %v\n", err)
			}
		}
	}
}

func (rw *RepoWatcher) ensureClone(ctx context.Context) error {
	if _, err := os.Stat(filepath.Join(rw.localPath, ".git")); err == nil {
		// Already cloned; just fetch.
		_, err := rw.pull(ctx)
		return err
	}
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth=1",
		"--branch", rw.ref, rw.repoURL, rw.localPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone: %w\n%s", err, out)
	}
	rw.lastCommit, _ = rw.headCommit(ctx)
	return nil
}

func (rw *RepoWatcher) pull(ctx context.Context) (changed bool, err error) {
	cmd := exec.CommandContext(ctx, "git", "-C", rw.localPath, "pull", "--ff-only", "origin", rw.ref)
	if out, err := cmd.CombinedOutput(); err != nil {
		return false, fmt.Errorf("git pull: %w\n%s", err, out)
	}
	head, err := rw.headCommit(ctx)
	if err != nil {
		return false, err
	}
	if head == rw.lastCommit {
		return false, nil
	}
	rw.lastCommit = head
	return true, nil
}

func (rw *RepoWatcher) headCommit(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", rw.localPath, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (rw *RepoWatcher) reconcileAll(ctx context.Context) error {
	return filepath.WalkDir(rw.localPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".yaml" && ext != ".yml" && ext != ".json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil // skip unreadable files
		}
		var w *types.Workload
		switch ext {
		case ".yaml", ".yml":
			src := ingestion.Detect(string(data))
			if src == ingestion.SourceCompose {
				w, err = ingestion.FromCompose(data)
			} else {
				w, err = ingestion.FromYAML(data)
			}
		case ".json":
			w, err = ingestion.FromJSON(data)
		}
		if err != nil || w == nil {
			return nil
		}
		if reconcileErr := rw.reconcile(ctx, w); reconcileErr != nil {
			fmt.Fprintf(os.Stderr, "gitops: reconcile %s: %v\n", path, reconcileErr)
		}
		return nil
	})
}
