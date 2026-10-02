package githubstack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Observation is remembered evidence, never authority for structure or
// deletion. A branch name can be reused, so the PR's identity stays with it.
type Observation struct {
	PullRequest      PullRequest `json:"pullRequest"`
	ObservedAt       time.Time   `json:"observedAt"`
	OpenCount        int         `json:"openCount"`
	MergeRequestedAt time.Time   `json:"mergeRequestedAt,omitempty"`
}

type ObservationReader interface {
	Load(context.Context) (map[string]Observation, error)
}

type Observer interface {
	Remember(context.Context, []PullRequest) error
	ObserveStates(context.Context, map[int]MergeState) error
	MergeRequested(context.Context, int, string) error
}

type CommonDirLocator interface {
	CommonDir(context.Context) (string, error)
}

// FileObservations is shared by linked worktrees and survives graph pruning.
// Its mutex protects concurrent clients in one process; atomic replacement
// protects readers in other processes. Cross-process writers are last-writer-wins,
// as with graph.json. Losing an observation loses context, never user work.
type FileObservations struct {
	Git CommonDirLocator
	Now func() time.Time
	mu  sync.Mutex
}

type observationDocument struct {
	Version  int                    `json:"schemaVersion"`
	Branches map[string]Observation `json:"branches"`
}

func (s *FileObservations) path(ctx context.Context) (string, error) {
	if s.Git == nil {
		return "", fmt.Errorf("PR observation store is not configured")
	}
	dir, err := s.Git.CommonDir(ctx)
	return filepath.Join(dir, "g2g", "pull-requests.json"), err
}

func (s *FileObservations) Load(ctx context.Context) (map[string]Observation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.path(ctx)
	if err != nil {
		return nil, err
	}
	return readObservations(path)
}

func readObservations(path string) (map[string]Observation, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]Observation{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read PR observations: %w", err)
	}
	var doc observationDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse PR observations: %w", err)
	}
	if doc.Version != 1 {
		return nil, fmt.Errorf("unsupported PR observation schema %d", doc.Version)
	}
	if doc.Branches == nil {
		doc.Branches = map[string]Observation{}
	}
	return doc.Branches, nil
}

func (s *FileObservations) update(ctx context.Context, change func(map[string]Observation, time.Time)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.path(ctx)
	if err != nil {
		return err
	}
	branches, err := readObservations(path)
	if err != nil {
		return err
	}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	change(branches, now.UTC())
	data, err := json.MarshalIndent(observationDocument{Version: 1, Branches: branches}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".pull-requests-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func (s *FileObservations) Remember(ctx context.Context, prs []PullRequest) error {
	if len(prs) == 0 {
		return nil
	}
	return s.update(ctx, func(branches map[string]Observation, now time.Time) {
		for branch, resolution := range ResolveHeads(prs) {
			pr := resolution.Latest
			if resolution.Open != nil {
				pr = resolution.Open
			}
			if pr == nil || pr.URL == "" {
				continue
			}
			old := branches[branch]
			seen := Observation{PullRequest: *pr, ObservedAt: now, OpenCount: resolution.OpenCount}
			if old.PullRequest.URL == pr.URL && pr.State == "OPEN" {
				seen.MergeRequestedAt = old.MergeRequestedAt
			}
			branches[branch] = seen
		}
	})
}

func (s *FileObservations) ObserveStates(ctx context.Context, states map[int]MergeState) error {
	if len(states) == 0 {
		return nil
	}
	return s.update(ctx, func(branches map[string]Observation, now time.Time) {
		for branch, seen := range branches {
			state, exists := states[seen.PullRequest.Number]
			if !exists || state.Head != branch {
				continue
			}
			seen.PullRequest.State, seen.PullRequest.HeadOID, seen.PullRequest.Base = state.State, state.HeadOID, state.Base
			seen.ObservedAt = now
			if state.State != "OPEN" {
				seen.OpenCount = 0
				seen.MergeRequestedAt = time.Time{}
			}
			branches[branch] = seen
		}
	})
}

func (s *FileObservations) MergeRequested(ctx context.Context, number int, head string) error {
	return s.update(ctx, func(branches map[string]Observation, now time.Time) {
		for branch, seen := range branches {
			if seen.PullRequest.Number != number || seen.PullRequest.HeadOID != head {
				continue
			}
			seen.MergeRequestedAt = now
			branches[branch] = seen
		}
	})
}
