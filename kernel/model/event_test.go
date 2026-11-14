package model_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// validEvent returns an event that passes validation, for tests to break.
func validEvent() model.NewEvent {
	return model.NewEvent{
		Type:    "kernel.test.recorded",
		Version: 1,
		Payload: json.RawMessage(`{"k":"v"}`),
		Actor:   model.Actor{Kind: model.ActorSystem},
	}
}

// requireCode fails unless err is a typed error with the wanted code.
func requireCode(t *testing.T, err error, want model.Code) {
	t.Helper()
	got, ok := model.CodeOf(err)
	if !ok || got != want {
		t.Fatalf("error = %v, want code %s", err, want)
	}
}

func TestNewEventValidateAcceptsValidEvents(t *testing.T) {
	t.Parallel()
	person := validEvent()
	person.Actor = model.Actor{Kind: model.ActorPerson, ParticipantID: "01900000-0000-7000-8000-000000000001"}
	person.SubjectRef = "participant:1"
	person.RequestID = "req-1"
	two := validEvent()
	two.Type = "kernel.recorded"
	for name, in := range map[string]model.NewEvent{
		"system": validEvent(), "person": person, "two segments": two,
	} {
		if err := in.Validate(); err != nil {
			t.Errorf("%s: Validate() = %v, want nil", name, err)
		}
	}
}

// TestNewEventValidateRejectsBadTypeVersionPayload guards criterion 4.
func TestNewEventValidateRejectsBadTypeVersionPayload(t *testing.T) {
	t.Parallel()
	cases := map[string]func(*model.NewEvent){
		"empty type":           func(e *model.NewEvent) { e.Type = "" },
		"one segment":          func(e *model.NewEvent) { e.Type = "recorded" },
		"uppercase":            func(e *model.NewEvent) { e.Type = "Kernel.test.recorded" },
		"trailing dot":         func(e *model.NewEvent) { e.Type = "kernel.test." },
		"leading dot":          func(e *model.NewEvent) { e.Type = ".kernel.test" },
		"double dot":           func(e *model.NewEvent) { e.Type = "kernel..test" },
		"digit first":          func(e *model.NewEvent) { e.Type = "kernel.1test" },
		"dash":                 func(e *model.NewEvent) { e.Type = "kernel.test-recorded" },
		"space":                func(e *model.NewEvent) { e.Type = "kernel.test recorded" },
		"version suffix":       func(e *model.NewEvent) { e.Type = "kernel.test.recorded.v1" },
		"too long":             func(e *model.NewEvent) { e.Type = "kernel." + strings.Repeat("a", 125) },
		"newline":              func(e *model.NewEvent) { e.Type = "kernel.test\n" },
		"version zero":         func(e *model.NewEvent) { e.Version = 0 },
		"version negative":     func(e *model.NewEvent) { e.Version = -1 },
		"payload nil":          func(e *model.NewEvent) { e.Payload = nil },
		"payload empty":        func(e *model.NewEvent) { e.Payload = json.RawMessage(``) },
		"payload null":         func(e *model.NewEvent) { e.Payload = json.RawMessage(`null`) },
		"payload array":        func(e *model.NewEvent) { e.Payload = json.RawMessage(`[1]`) },
		"payload scalar":       func(e *model.NewEvent) { e.Payload = json.RawMessage(`"x"`) },
		"payload number":       func(e *model.NewEvent) { e.Payload = json.RawMessage(`1`) },
		"payload malformed":    func(e *model.NewEvent) { e.Payload = json.RawMessage(`{"k":`) },
		"payload trailing":     func(e *model.NewEvent) { e.Payload = json.RawMessage(`{} {}`) },
		"blank request id":     func(e *model.NewEvent) { e.RequestID = "  " },
		"blank subject":        func(e *model.NewEvent) { e.SubjectRef = "\t" },
		"participant not uuid": func(e *model.NewEvent) { e.Actor.ParticipantID = "abc" },
		"participant nil uuid": func(e *model.NewEvent) { e.Actor.ParticipantID = "00000000-0000-0000-0000-000000000000" },
	}
	for name, mutate := range cases {
		in := validEvent()
		mutate(&in)
		if err := in.Validate(); err == nil {
			t.Errorf("%s: Validate() = nil, want invalid_input", name)
		} else {
			requireCode(t, err, model.CodeInvalidInput)
		}
	}
}

// TestNewEventValidateRejectsUnknownActorKind guards criterion 5.
func TestNewEventValidateRejectsUnknownActorKind(t *testing.T) {
	t.Parallel()
	for _, kind := range []model.ActorKind{"", "robot", "Person", "organization"} {
		in := validEvent()
		in.Actor = model.Actor{Kind: kind, ParticipantID: "01900000-0000-7000-8000-000000000001"}
		requireCode(t, in.Validate(), model.CodeInvalidInput)
	}
}

// TestNewEventWithoutActorRejected guards criterion 6 at the model layer.
func TestNewEventWithoutActorRejected(t *testing.T) {
	t.Parallel()
	for _, kind := range []model.ActorKind{model.ActorPerson, model.ActorAgent, model.ActorAPIKey} {
		in := validEvent()
		in.Actor = model.Actor{Kind: kind}
		requireCode(t, in.Validate(), model.CodePermissionDenied)
	}
}

func TestActorKindValid(t *testing.T) {
	t.Parallel()
	for k, want := range map[model.ActorKind]bool{
		model.ActorPerson: true, model.ActorAgent: true, model.ActorAPIKey: true, model.ActorSystem: true,
		"": false, "robot": false,
	} {
		if got := k.Valid(); got != want {
			t.Errorf("ActorKind(%q).Valid() = %v, want %v", k, got, want)
		}
	}
}
