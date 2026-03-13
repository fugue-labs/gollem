package temporal

import (
	"encoding/json"
	"errors"
	"reflect"
)

// DepsCodec customizes serialization for workflow dep overrides.
type DepsCodec interface {
	MarshalDeps(deps any) ([]byte, error)
	UnmarshalDeps(data []byte, target any) error
}

// JSONDepsCodec marshals deps with encoding/json.
type JSONDepsCodec struct{}

func (JSONDepsCodec) MarshalDeps(deps any) ([]byte, error) {
	if deps == nil {
		return nil, nil
	}
	return json.Marshal(deps)
}

func (JSONDepsCodec) UnmarshalDeps(data []byte, target any) error {
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, target)
}

// MarshalDeps serializes workflow dep overrides using the TemporalAgent codec.
func (ta *TemporalAgent[T]) MarshalDeps(deps any) ([]byte, error) {
	if ta == nil {
		return nil, errors.New("nil TemporalAgent")
	}
	if deps == nil {
		return nil, nil
	}
	return ta.depsCodec.MarshalDeps(deps)
}

func (ta *TemporalAgent[T]) resolveDeps(data []byte) (any, error) {
	return decodeTemporalDeps(ta.depsCodec, ta.depsType, ta.runtime.AgentDeps, data)
}

func decodeTemporalDeps(codec DepsCodec, depsType reflect.Type, defaultDeps any, data []byte) (any, error) {
	if len(data) == 0 {
		return defaultDeps, nil
	}
	if codec == nil {
		codec = JSONDepsCodec{}
	}
	if depsType == nil {
		return nil, errors.New("workflow deps provided but no default deps or WithDepsPrototype was configured")
	}

	if depsType.Kind() == reflect.Ptr {
		target := reflect.New(depsType.Elem())
		if err := codec.UnmarshalDeps(data, target.Interface()); err != nil {
			return nil, err
		}
		return target.Interface(), nil
	}

	target := reflect.New(depsType)
	if err := codec.UnmarshalDeps(data, target.Interface()); err != nil {
		return nil, err
	}
	return target.Elem().Interface(), nil
}
