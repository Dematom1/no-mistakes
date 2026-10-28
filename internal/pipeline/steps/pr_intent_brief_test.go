package steps

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/agent"
	"github.com/kunchenguid/no-mistakes/internal/config"
	"github.com/kunchenguid/no-mistakes/internal/pipeline"
	"github.com/kunchenguid/no-mistakes/internal/scm"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

func TestRenderPRIntentBriefUsesConfiguredSectionsAndOrder(t *testing.T) {
	got, err := renderPRIntentBrief(prIntentBrief{
		Goal:       "Give reviewers a clear migration path",
		Why:        []string{"Reduce upgrade mistakes"},
		Scope:      []string{"Add the validation command"},
		OutOfScope: []string{"Do not change existing defaults"},
	}, []string{"scope", "why"})
	if err != nil {
		t.Fatal(err)
	}
	want := "Give reviewers a clear migration path\n\n### Scope\n- Add the validation command\n\n### Why\n- Reduce upgrade mistakes"
	if got != want {
		t.Fatalf("rendered brief = %q, want %q", got, want)
	}
	if strings.Contains(got, "Out of scope") {
		t.Fatalf("unselected section was rendered: %s", got)
	}
}

func TestPRStep_BriefIsSeparateFromStoredIntent(t *testing.T) {
	t.Parallel()
	dir, baseSHA, headSHA := setupGitRepo(t)
	const source = "Add the feature; internal apply run 123 and operator title are not reviewer context."
	ag := &mockAgent{name: "test", runFn: func(_ context.Context, opts agent.RunOpts) (*agent.Result, error) {
		if !strings.Contains(opts.Prompt, source) || !strings.Contains(opts.Prompt, "intent_brief") {
			t.Fatalf("PR draft prompt lacks original review criteria or separate brief instruction: %s", opts.Prompt)
		}
		return &agent.Result{Output: json.RawMessage(`{"title":"feat: add validation","body":"## What Changed\n\n- Add validation","intent_brief":{"goal":"Validate the submitted change","why":["Catch mistakes before execution"],"scope":["Check configured terms"],"out_of_scope":["Keep the default behavior unchanged"]}}`)}, nil
	}}
	sctx := newTestContextWithDBRecords(t, ag, dir, baseSHA, headSHA, config.Commands{})
	sctx.UserIntent = source
	sctx.Config.PR.Intent.Format = "brief"
	sctx.Config.PR.Intent.Sections = []string{"why", "scope", "out_of_scope"}
	insertCompletedStep(t, sctx, types.StepReview, "", "")

	content, err := (&PRStep{}).buildPRContent(sctx, "feature", "main", baseSHA, scm.ProviderGitHub, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## Intent\n\nValidate the submitted change",
		"### Why\n- Catch mistakes before execution",
		"### Scope\n- Check configured terms",
		"### Out of scope\n- Keep the default behavior unchanged",
		"## What Changed",
	} {
		if !strings.Contains(content.Body, want) {
			t.Errorf("body missing %q:\n%s", want, content.Body)
		}
	}
	if strings.Contains(content.Body, source) || strings.Contains(content.Body, "internal apply run 123") {
		t.Fatalf("raw intent leaked into the public body:\n%s", content.Body)
	}
	if strings.Count(content.Body, "## Intent") != 1 || strings.Count(content.Body, pipelineAttestationCommentPrefix) != 1 {
		t.Fatalf("Intent/attestation count is wrong:\n%s", content.Body)
	}
	if sctx.UserIntent != source {
		t.Fatalf("draft changed review intent: got %q, want %q", sctx.UserIntent, source)
	}
}

func TestPRStep_TemplatePublishesTheSameStructuredBrief(t *testing.T) {
	t.Parallel()
	sctx, ag, _ := templateTestContext(t)
	const source = "The internal task ID task-735 is not reviewer context."
	sctx.UserIntent = source
	sctx.Config.PR.Intent.Format = "brief"
	sctx.Config.PR.Intent.Sections = []string{"scope", "why"}
	ag.runFn = func(_ context.Context, opts agent.RunOpts) (*agent.Result, error) {
		if !strings.Contains(opts.Prompt, source) || !strings.Contains(opts.Prompt, "intent_brief") {
			t.Fatalf("template prompt lacks original criteria or brief instruction: %s", opts.Prompt)
		}
		payload, err := json.Marshal(prContent{
			Title: "feat: fill template",
			Body:  filledPRTemplate,
			IntentBrief: &prIntentBrief{
				Goal:  "Fill the contribution template",
				Why:   []string{"Make changes easier to review"},
				Scope: []string{"Preserve repository headings"},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return &agent.Result{Output: payload}, nil
	}

	content, err := (&PRStep{}).buildPRContent(sctx, "feature", "main", sctx.Run.BaseSHA, scm.ProviderGitHub, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content.Body, "## Intent\n\nFill the contribution template\n\n### Scope\n- Preserve repository headings\n\n### Why\n- Make changes easier to review") {
		t.Fatalf("template appendix did not use the structured brief:\n%s", content.Body)
	}
	if strings.Contains(content.Body, source) || strings.Count(content.Body, "## Intent") != 1 || strings.Count(content.Body, pipelineAttestationCommentPrefix) != 1 {
		t.Fatalf("template publication leaked source or duplicated managed sections:\n%s", content.Body)
	}
}

func TestPRStep_OwnedAppendixDraftsBriefWithoutReplacingAuthorNarrative(t *testing.T) {
	t.Parallel()
	dir, baseSHA, headSHA := setupGitRepo(t)
	const source = "Keep raw internal run run-411 out of the public brief."
	ag := &mockAgent{name: "test", runFn: func(_ context.Context, opts agent.RunOpts) (*agent.Result, error) {
		if !strings.Contains(opts.Prompt, source) || !strings.Contains(opts.Prompt, "intent_brief") {
			t.Fatalf("brief-only prompt lacks review criteria or output contract: %s", opts.Prompt)
		}
		return &agent.Result{Output: json.RawMessage(`{"intent_brief":{"goal":"Keep the release check reliable","why":["Catch invalid changes early"]}}`)}, nil
	}}
	sctx := newTestContextWithDBRecords(t, ag, dir, baseSHA, headSHA, config.Commands{})
	sctx.UserIntent = source
	sctx.Config.PR.Intent.Format = "brief"
	sctx.Config.PR.Intent.Sections = []string{"why"}
	insertCompletedStep(t, sctx, types.StepReview, "", "")
	step := &PRStep{}
	if err := step.draftPRIntentBrief(sctx, "feature", "main", baseSHA); err != nil {
		t.Fatal(err)
	}
	appendix, err := step.buildPRAppendix(sctx, scm.ProviderGitHub)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(appendix, "## Intent\n\nKeep the release check reliable\n\n### Why\n- Catch invalid changes early") {
		t.Fatalf("owned appendix lacks brief:\n%s", appendix)
	}
	if strings.Contains(appendix, source) || strings.Count(appendix, "## Intent") != 1 || strings.Count(appendix, pipelineAttestationCommentPrefix) != 1 {
		t.Fatalf("owned appendix leaked source or duplicated managed sections:\n%s", appendix)
	}
}

func TestPRStep_BriefOmissionSkipsGenerationAndPublication(t *testing.T) {
	t.Parallel()
	dir, baseSHA, headSHA := setupGitRepo(t)
	const source = "Private source that must not be drafted or published"
	ag := &mockAgent{name: "test", runFn: func(_ context.Context, opts agent.RunOpts) (*agent.Result, error) {
		if strings.Contains(opts.Prompt, source) || strings.Contains(opts.Prompt, "intent_brief") {
			t.Fatalf("omitted intent reached PR drafting prompt: %s", opts.Prompt)
		}
		return &agent.Result{Output: json.RawMessage(`{"title":"feat: add validation","body":"## What Changed\n\n- Add validation"}`)}, nil
	}}
	sctx := newTestContextWithDBRecords(t, ag, dir, baseSHA, headSHA, config.Commands{})
	sctx.UserIntent = source
	sctx.Run.OmitIntent = true
	sctx.Config.PR.Intent.Format = "brief"

	content, err := (&PRStep{}).buildPRContent(sctx, "feature", "main", baseSHA, scm.ProviderGitHub, 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(content.Body, "## Intent") || strings.Contains(content.Body, source) {
		t.Fatalf("omitted intent was published:\n%s", content.Body)
	}
}

func TestPRStep_BriefModeFailsClosedWithoutStructuredOutput(t *testing.T) {
	t.Parallel()
	dir, baseSHA, headSHA := setupGitRepo(t)
	const source = "private intent that must not be used as fallback"
	ag := &mockAgent{name: "test", runFn: func(context.Context, agent.RunOpts) (*agent.Result, error) {
		return &agent.Result{Output: json.RawMessage(`{"title":"feat: add validation","body":"## What Changed\\n\\n- Add validation"}`)}, nil
	}}
	sctx := newTestContextWithDBRecords(t, ag, dir, baseSHA, headSHA, config.Commands{})
	sctx.UserIntent = source
	sctx.Config.PR.Intent.Format = "brief"
	if _, err := (&PRStep{}).buildPRContent(sctx, "feature", "main", baseSHA, scm.ProviderGitHub, 0); err == nil || strings.Contains(err.Error(), source) {
		t.Fatalf("buildPRContent error = %v, want non-disclosing fail-closed error", err)
	}
}

func TestFallbackPRContentRefusesRawIntentInBriefMode(t *testing.T) {
	sctx := &pipeline.StepContext{Config: &config.Config{PR: config.PR{Intent: config.PRIntent{Format: "brief"}}}, UserIntent: "private raw intent"}
	if _, err := fallbackPRContent(sctx, "", "", "", "", 0, scm.ProviderGitHub); err == nil || strings.Contains(err.Error(), "private raw intent") {
		t.Fatalf("fallback error = %v, want a non-disclosing fail-closed error", err)
	}
}
