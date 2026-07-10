package appserver

import (
	"time"

	appcache "github.com/fugue-labs/gollem/appserver/cache"
	"github.com/fugue-labs/gollem/appserver/store"
	toolfs "github.com/fugue-labs/gollem/appserver/tools/fs"
)

type cacheBenchmarkNotificationParams struct {
	Passed        bool                     `json:"passed"`
	TargetHitRate float64                  `json:"targetHitRate"`
	Totals        appcache.ProviderStats   `json:"totals"`
	Providers     []appcache.ProviderStats `json:"providers"`
	At            time.Time                `json:"at"`
}

func (s *Server) publishFileChanged(operation, path, destination string) {
	s.PublishNotification("fs/changed", fileChangedParams{
		Path:        path,
		Destination: destination,
		Operation:   operation,
		At:          time.Now().UTC(),
	})
}

func (s *Server) publishWatchChanged(event toolfs.WatchEvent) {
	s.PublishNotification("fs/changed", fsWatchChangedParams{
		WatchID:      event.WatchID,
		ChangedPaths: append([]string(nil), event.ChangedPaths...),
	})
}

func (s *Server) publishThreadNotification(method string, thread *store.Thread) {
	if thread == nil {
		return
	}
	s.PublishNotification(method, threadNotificationParams{
		ThreadID: thread.ID,
		Status:   thread.Status,
		Thread:   thread,
		At:       time.Now().UTC(),
	})
}

func (s *Server) publishThreadClosed(threadID string) {
	if threadID == "" {
		return
	}
	s.PublishNotification("thread/closed", threadClosedNotificationParams{ThreadID: threadID})
	s.PublishNotification("thread/status/changed", threadNotLoadedStatusNotificationParams{
		ThreadID: threadID,
		Status:   map[string]string{"type": "notLoaded"},
		At:       time.Now().UTC(),
	})
}

func (s *Server) publishThreadGoalNotification(method string, thread *store.Thread, goal any) {
	if thread == nil {
		return
	}
	s.PublishNotification(method, threadGoalNotificationParams{
		ThreadID: thread.ID,
		Goal:     goal,
		Thread:   thread,
		At:       time.Now().UTC(),
	})
}

func (s *Server) publishThreadNameNotification(thread *store.Thread) {
	if thread == nil {
		return
	}
	s.PublishNotification("thread/name/updated", threadNameNotificationParams{
		ThreadID: thread.ID,
		Name:     thread.Title,
		Thread:   thread,
		At:       time.Now().UTC(),
	})
}

func (s *Server) publishCacheBenchmarkCompleted(result appcache.BenchmarkResponse) {
	providers := make([]appcache.ProviderStats, 0, len(result.Providers))
	for _, provider := range result.Providers {
		providers = append(providers, appcache.ProviderStats{
			Provider:      provider.Provider,
			TotalRequests: provider.TotalRequests,
			Hits:          provider.Hits,
			Misses:        provider.Misses,
			HitRate:       provider.HitRate,
		})
	}
	s.PublishNotification("cache/benchmark/completed", cacheBenchmarkNotificationParams{
		Passed:        result.Passed,
		TargetHitRate: result.TargetHitRate,
		Totals:        result.Totals,
		Providers:     providers,
		At:            time.Now().UTC(),
	})
}
