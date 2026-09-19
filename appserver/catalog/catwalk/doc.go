// Package catwalk mirrors the provider and model registry published by
// Charm's catwalk service (https://github.com/charmbracelet/catwalk), the
// same registry Crush uses. It ships an embedded snapshot so the catalog works
// offline, a small HTTP client for refreshing from the hosted registry, and a
// disk cache so refreshed data survives restarts.
//
// The package intentionally has no dependency on the catwalk Go module: the
// wire format is plain JSON, and the types below carry the same field names.
package catwalk
