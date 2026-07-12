package contract

import (
	"errors"
	"strings"
	"testing"

	"github.com/gopact-ai/gopact/agent"
)

func TestValidatorAccumulatesDeclarativeContracts(t *testing.T) {
	var pointer *int
	err := New("router").
		Identity("agent", agent.Identity{Name: "router"}).
		Present("directory", pointer).
		Required("selection child", "").
		NonEmpty("candidates", 0).
		Positive("max rounds", 0).
		OptionalJSON("tool schema", []byte(`{"type":`)).
		Unique("child", "worker").
		Unique("child", "worker").
		Err()
	if err == nil {
		t.Fatal("Err() = nil")
	}
	if !errors.Is(err, agent.ErrInvalidIdentity) {
		t.Fatalf("Err() = %v, want ErrInvalidIdentity", err)
	}
	for _, text := range []string{
		"router: agent: invalid identity: agent name, description, and version are required",
		"router: directory is nil",
		"router: selection child is required",
		"router: candidates must not be empty",
		"router: max rounds must be positive",
		"router: tool schema contains invalid JSON",
		`router: duplicate child "worker"`,
	} {
		if !strings.Contains(err.Error(), text) {
			t.Fatalf("error = %q, want %q", err, text)
		}
	}
}

func TestValidatorAcceptsValidValues(t *testing.T) {
	value := 1
	err := New("agent").
		Identity("child", agent.Identity{Name: "worker", Description: "works", Version: "v1"}).
		Present("target", &value).
		Required("name", "worker").
		NonEmpty("steps", 1).
		Positive("limit", 1).
		OptionalJSON("schema", nil).
		Unique("step", "one").
		Unique("step", "two").
		Err()
	if err != nil {
		t.Fatalf("Err() = %v", err)
	}
}

func TestIsNilHandlesTypedNilAndValues(t *testing.T) {
	var pointer *int
	var function func()
	var mapping map[string]int
	var channel chan int
	var slice []int
	for name, value := range map[string]any{
		"nil":      nil,
		"pointer":  pointer,
		"function": function,
		"map":      mapping,
		"channel":  channel,
		"slice":    slice,
	} {
		if !IsNil(value) {
			t.Errorf("IsNil(%s) = false", name)
		}
	}
	value := 1
	for name, candidate := range map[string]any{
		"integer":  0,
		"struct":   struct{}{},
		"pointer":  &value,
		"function": func() {},
		"map":      map[string]int{},
		"channel":  make(chan int),
		"slice":    []int{},
	} {
		if IsNil(candidate) {
			t.Errorf("IsNil(%s) = true", name)
		}
	}
}
