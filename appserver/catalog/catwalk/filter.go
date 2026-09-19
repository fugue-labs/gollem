package catwalk

// SupportedType reports whether Gollem has a provider implementation that can
// drive providers of the given catwalk type.
func SupportedType(t Type) bool {
	switch t {
	case TypeAnthropic, TypeOpenAI, TypeOpenAICompat, TypeOpenRouter, TypeVertexAI:
		return true
	default:
		return false
	}
}

// FilterSupported returns the providers whose type Gollem supports and whose
// credentials come from the environment (a literal API key in the registry
// would be a shared secret; a missing key with no way to supply one cannot be
// used either). Providers without an API key entry are kept only for types
// that authenticate out of band (Vertex AI uses application credentials).
func FilterSupported(providers []Provider) []Provider {
	kept := make([]Provider, 0, len(providers))
	for _, provider := range providers {
		if !SupportedType(provider.Type) {
			continue
		}
		if provider.Type != TypeVertexAI {
			if _, ok := EnvReference(provider.APIKey); !ok {
				continue
			}
		}
		if len(provider.Models) == 0 {
			continue
		}
		kept = append(kept, cloneProvider(provider))
	}
	return kept
}
