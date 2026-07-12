// Package contract provides private declarative validation for official Agents.
package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/gopact-ai/gopact/agent"
)

// Validator accumulates construction and value-contract violations.
type Validator struct {
	scope    string
	problems []error
	seen     map[string]map[string]struct{}
}

// New creates an empty validator for one package or operation scope.
func New(scope string) *Validator {
	return &Validator{scope: scope}
}

// Identity requires a complete immutable Agent identity.
func (validator *Validator) Identity(subject string, identity agent.Identity) *Validator {
	return validator.Check(
		identity.Name != "" && identity.Description != "" && identity.Version != "",
		"%w: %s name, description, and version are required",
		agent.ErrInvalidIdentity,
		subject,
	)
}

// Present requires a non-nil value, including values held by interfaces.
func (validator *Validator) Present(name string, value any) *Validator {
	return validator.Check(!IsNil(value), "%s is nil", name)
}

// Required requires a non-empty string value.
func (validator *Validator) Required(name, value string) *Validator {
	return validator.Check(value != "", "%s is required", name)
}

// NonEmpty requires a collection to contain at least one item.
func (validator *Validator) NonEmpty(name string, size int) *Validator {
	return validator.Check(size > 0, "%s must not be empty", name)
}

// Positive requires a strictly positive integer.
func (validator *Validator) Positive(name string, value int) *Validator {
	return validator.Check(value > 0, "%s must be positive", name)
}

// OptionalJSON validates a non-empty JSON value.
func (validator *Validator) OptionalJSON(name string, value []byte) *Validator {
	return validator.Check(len(value) == 0 || json.Valid(value), "%s contains invalid JSON", name)
}

// Unique requires each non-empty value in one named namespace to appear once.
func (validator *Validator) Unique(name, value string) *Validator {
	if value == "" {
		return validator
	}
	if validator.seen == nil {
		validator.seen = make(map[string]map[string]struct{})
	}
	values := validator.seen[name]
	if values == nil {
		values = make(map[string]struct{})
		validator.seen[name] = values
	}
	if _, exists := values[value]; exists {
		validator.add(fmt.Errorf("duplicate %s %q", name, value))
		return validator
	}
	values[value] = struct{}{}
	return validator
}

// Check adds a formatted violation when valid is false.
func (validator *Validator) Check(valid bool, format string, arguments ...any) *Validator {
	if !valid {
		validator.add(fmt.Errorf(format, arguments...))
	}
	return validator
}

// Err returns every accumulated violation as one error.
func (validator *Validator) Err() error {
	return errors.Join(validator.problems...)
}

func (validator *Validator) add(problem error) {
	if problem == nil {
		return
	}
	if validator.scope != "" {
		problem = fmt.Errorf("%s: %w", validator.scope, problem)
	}
	validator.problems = append(validator.problems, problem)
}

// IsNil reports nil and typed-nil reference values without panicking on values.
func IsNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
