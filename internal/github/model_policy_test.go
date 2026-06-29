package github

import (
	"testing"

	"github.com/tiendv89/workspace-github-adapter/internal/domain"
)

// allPhases builds a map with all five canonical phases using the given model slug.
//
//nolint:unparam
func allPhases(slug string) map[string]modelPhasePolicyYAML {
	entry := modelPhasePolicyYAML{Allowed: []string{slug}, Default: slug}
	return map[string]modelPhasePolicyYAML{
		"implementation":      entry,
		"self_review":         entry,
		"pr_description":      entry,
		"suggested_next_step": entry,
		"conflict_resolution": entry,
	}
}

func TestParseModelPolicy_Nil(t *testing.T) {
	got, err := parseModelPolicy(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil snapshot for absent model_policy")
	}
}

func TestParseModelPolicy_ValidSingleModel(t *testing.T) {
	raw := allPhases("claude-sonnet-4-6")
	got, err := parseModelPolicy(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil snapshot")
	}
	if len(got.Phases) != 5 {
		t.Fatalf("expected 5 phases, got %d", len(got.Phases))
	}
	pp := got.Phases["implementation"]
	if len(pp.Allowed) != 1 || pp.Allowed[0] != "claude-sonnet-4-6" {
		t.Errorf("unexpected allowed: %v", pp.Allowed)
	}
	if pp.Default != "claude-sonnet-4-6" {
		t.Errorf("unexpected default: %s", pp.Default)
	}
}

func TestParseModelPolicy_MultipleAllowed(t *testing.T) {
	raw := allPhases("claude-sonnet-4-6")
	raw["implementation"] = modelPhasePolicyYAML{
		Allowed: []string{"claude-haiku-4-5-20251001", "claude-sonnet-4-6"},
		Default: "claude-sonnet-4-6",
	}
	got, err := parseModelPolicy(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pp := got.Phases["implementation"]
	if len(pp.Allowed) != 2 {
		t.Errorf("expected 2 allowed, got %d", len(pp.Allowed))
	}
	if pp.Default != "claude-sonnet-4-6" {
		t.Errorf("unexpected default: %s", pp.Default)
	}
}

func TestParseModelPolicy_DedupeAllowed(t *testing.T) {
	raw := allPhases("claude-sonnet-4-6")
	raw["implementation"] = modelPhasePolicyYAML{
		Allowed: []string{"claude-sonnet-4-6", "claude-sonnet-4-6", "claude-haiku-4-5-20251001"},
		Default: "claude-sonnet-4-6",
	}
	got, err := parseModelPolicy(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pp := got.Phases["implementation"]
	if len(pp.Allowed) != 2 {
		t.Errorf("expected 2 after dedupe, got %d: %v", len(pp.Allowed), pp.Allowed)
	}
	if pp.Allowed[0] != "claude-sonnet-4-6" {
		t.Error("first occurrence should be preserved")
	}
}

func TestParseModelPolicy_UnknownPhase(t *testing.T) {
	raw := allPhases("claude-sonnet-4-6")
	raw["typo_phase"] = modelPhasePolicyYAML{Allowed: []string{"claude-sonnet-4-6"}, Default: "claude-sonnet-4-6"}
	_, se := parseModelPolicy(raw)
	if se == nil {
		t.Fatal("expected error for unknown phase")
	}
	if se.Code != domain.ErrModelPolicyUnknownPhase {
		t.Errorf("unexpected code: %s", se.Code)
	}
}

func TestParseModelPolicy_MissingPhase(t *testing.T) {
	raw := allPhases("claude-sonnet-4-6")
	delete(raw, "self_review")
	_, se := parseModelPolicy(raw)
	if se == nil {
		t.Fatal("expected error for missing phase")
	}
	if se.Code != domain.ErrModelPolicyMissingPhase {
		t.Errorf("unexpected code: %s", se.Code)
	}
}

func TestParseModelPolicy_EmptyAllowed(t *testing.T) {
	raw := allPhases("claude-sonnet-4-6")
	raw["implementation"] = modelPhasePolicyYAML{Allowed: []string{}, Default: "claude-sonnet-4-6"}
	_, se := parseModelPolicy(raw)
	if se == nil {
		t.Fatal("expected error for empty allowed")
	}
	if se.Code != domain.ErrModelPolicyInvalidAllowed {
		t.Errorf("unexpected code: %s", se.Code)
	}
}

func TestParseModelPolicy_EmptyDefault(t *testing.T) {
	raw := allPhases("claude-sonnet-4-6")
	raw["implementation"] = modelPhasePolicyYAML{Allowed: []string{"claude-sonnet-4-6"}, Default: ""}
	_, se := parseModelPolicy(raw)
	if se == nil {
		t.Fatal("expected error for empty default")
	}
	if se.Code != domain.ErrModelPolicyInvalidDefault {
		t.Errorf("unexpected code: %s", se.Code)
	}
}

func TestParseModelPolicy_DefaultNotInAllowed(t *testing.T) {
	raw := allPhases("claude-sonnet-4-6")
	raw["implementation"] = modelPhasePolicyYAML{
		Allowed: []string{"claude-haiku-4-5-20251001"},
		Default: "claude-sonnet-4-6",
	}
	_, se := parseModelPolicy(raw)
	if se == nil {
		t.Fatal("expected error for default not in allowed")
	}
	if se.Code != domain.ErrModelPolicyDefaultNotAllowed {
		t.Errorf("unexpected code: %s", se.Code)
	}
}
