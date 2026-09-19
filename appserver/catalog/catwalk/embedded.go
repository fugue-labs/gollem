package catwalk

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// providersJSON is a snapshot of the hosted registry, filtered to the provider
// types Gollem can drive. Refresh it with:
//
//	go generate ./appserver/catalog/catwalk
//
//go:generate go run ./internal/cmd/sync
//go:embed providers.json
var providersJSON []byte

// snapshotJSON records when providers.json was captured so a cache written
// by an older build never shadows a newer embedded snapshot.
//
//go:embed snapshot.json
var snapshotJSON []byte

// SnapshotInfo describes the embedded registry snapshot.
type SnapshotInfo struct {
	GeneratedAt time.Time `json:"generatedAt"`
	Source      string    `json:"source,omitempty"`
}

var (
	embeddedOnce      sync.Once
	embeddedProviders []Provider
	embeddedInfo      SnapshotInfo
	embeddedErr       error
)

// EmbeddedSnapshot reports when the embedded registry was captured.
func EmbeddedSnapshot() (SnapshotInfo, error) {
	embeddedOnce.Do(loadEmbedded)
	return embeddedInfo, embeddedErr
}

func loadEmbedded() {
	embeddedProviders, embeddedErr = Parse(providersJSON)
	if embeddedErr != nil {
		return
	}
	if err := json.Unmarshal(snapshotJSON, &embeddedInfo); err != nil {
		embeddedErr = fmt.Errorf("catwalk: decode snapshot metadata: %w", err)
		return
	}
	if embeddedInfo.GeneratedAt.IsZero() {
		embeddedErr = errors.New("catwalk: snapshot metadata has no generatedAt")
	}
}

// Embedded returns a copy of the snapshot compiled into the binary.
func Embedded() ([]Provider, error) {
	embeddedOnce.Do(loadEmbedded)
	if embeddedErr != nil {
		return nil, embeddedErr
	}
	return Clone(embeddedProviders), nil
}

// Parse decodes a catwalk provider list and rejects structurally unusable
// entries (missing ids, duplicate provider ids, models without ids).
func Parse(data []byte) ([]Provider, error) {
	var providers []Provider
	if err := json.Unmarshal(data, &providers); err != nil {
		return nil, fmt.Errorf("catwalk: decode providers: %w", err)
	}
	if err := Validate(providers); err != nil {
		return nil, err
	}
	return providers, nil
}

// Validate checks the invariants Parse enforces.
func Validate(providers []Provider) error {
	seen := make(map[string]struct{}, len(providers))
	for i, provider := range providers {
		if provider.ID == "" {
			return fmt.Errorf("catwalk: provider %d has no id", i)
		}
		if _, dup := seen[provider.ID]; dup {
			return fmt.Errorf("catwalk: duplicate provider id %q", provider.ID)
		}
		seen[provider.ID] = struct{}{}
		modelIDs := make(map[string]struct{}, len(provider.Models))
		for j, model := range provider.Models {
			if model.ID == "" {
				return fmt.Errorf("catwalk: provider %q model %d has no id", provider.ID, j)
			}
			if _, dup := modelIDs[model.ID]; dup {
				return fmt.Errorf("catwalk: provider %q has duplicate model id %q", provider.ID, model.ID)
			}
			modelIDs[model.ID] = struct{}{}
		}
	}
	return nil
}
