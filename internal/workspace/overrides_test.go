package workspace

import (
	"context"
	"os"
	"strings"
	"testing"
)

func firstRulePayload(t *testing.T, w *Workspace) string {
	t.Helper()
	rules, err := w.Client().Rules(context.Background())
	if err != nil || len(rules) == 0 {
		t.Fatalf("rules: %v %v", rules, err)
	}
	return rules[0].Payload
}

func TestOverridesReachRunningCore(t *testing.T) {
	w := openTestWorkspace(t, "process")
	ctx := context.Background()
	src := t.TempDir()
	a, err := w.AddLocal(ctx, profileFile(t, src, "a", "node-a"), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer w.Stop(ctx)

	o, err := w.AddOverride("自定义规则")
	if err != nil {
		t.Fatal(err)
	}
	// New overrides start disabled with a comment-only template.
	if o.Enabled || firstRulePayload(t, w) == "tlash-test.example" {
		t.Fatal("new override should not apply")
	}
	if err := os.WriteFile(w.Overrides.ContentPath(o.ID), []byte("+rules:\n  - DOMAIN,tlash-test.example,REJECT\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := w.SetOverrideEnabled(ctx, o.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := firstRulePayload(t, w); got != "tlash-test.example" {
		t.Fatalf("first rule = %q", got)
	}

	// A broken edit is rejected and the running rules stay as they were.
	if err := os.WriteFile(w.Overrides.ContentPath(o.ID), []byte("+rules: not-a-list\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := w.ReapplyAfterOverrideEdit(ctx, o.ID); err == nil || !strings.Contains(err.Error(), "列表") {
		t.Fatalf("want list error, got %v", err)
	}
	if got := firstRulePayload(t, w); got != "tlash-test.example" {
		t.Fatalf("rules changed after rejected edit: %q", got)
	}

	// Scoping to another profile removes it from this one.
	if err := os.WriteFile(w.Overrides.ContentPath(o.ID), []byte("+rules:\n  - DOMAIN,tlash-test.example,REJECT\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := w.SetOverrideScope(ctx, o.ID, []string{"someone-else"}); err != nil {
		t.Fatal(err)
	}
	if got := firstRulePayload(t, w); got == "tlash-test.example" {
		t.Fatal("override scoped away still applied")
	}
	if err := w.SetOverrideScope(ctx, o.ID, []string{a.ID}); err != nil {
		t.Fatal(err)
	}
	if got := firstRulePayload(t, w); got != "tlash-test.example" {
		t.Fatal("override scoped to active profile not applied")
	}
	if err := w.DeleteOverride(ctx, o.ID); err != nil {
		t.Fatal(err)
	}
	if got := firstRulePayload(t, w); got == "tlash-test.example" {
		t.Fatal("deleted override still applied")
	}
}
