// Package catalog exposes provider-neutral app-server metadata for models,
// providers, and built-in tools.
//
// Provider and model data comes from the catwalk registry (see the catwalk
// subpackage): an embedded snapshot is always available, a per-user cache
// carries the last successful refresh across restarts, and Refresh pulls the
// hosted registry. Local configuration (which API keys are set, the loopback
// OpenAI-compatible endpoint) is layered on top at construction and refresh
// time. Third-party registry providers that Gollem drives through its generic
// Anthropic or OpenAI transports are described by RuntimeEndpoint.
package catalog
