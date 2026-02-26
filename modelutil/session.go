package modelutil

import "github.com/fugue-labs/gollem/core"

type modelSessionCloner interface {
	NewSession() core.Model
}

// NewSessionModel returns an isolated model session when supported by the
// provided model. When unsupported, it returns the original model.
func NewSessionModel(model core.Model) core.Model {
	if model == nil {
		return nil
	}
	if cloner, ok := model.(modelSessionCloner); ok {
		return cloner.NewSession()
	}
	return model
}
