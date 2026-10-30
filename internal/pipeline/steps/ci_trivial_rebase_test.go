package steps

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/pipeline"
	"github.com/kunchenguid/no-mistakes/internal/scm"
)

func TestTrivialRebasePatchIDProof(t *testing.T) {
	t.Run("same patch on descendant base", func(t *testing.T) {
		sctx, _ := behindBaseContext(t, &mockAgent{name: "test"}, "MERGEABLE BEHIND")
		oldBase, oldHead := sctx.Run.BaseSHA, sctx.Run.HeadSHA
		newBase := advanceBaseAndRebase(t, sctx)
		newHead := gitCmd(t, sctx.WorkDir, "rev-parse", "HEAD")
		if gap := trivialRebasePatchIDGap(sctx, oldBase, oldHead, newBase, newHead); gap != "" {
			t.Fatalf("trivial rebase rejected: %s", gap)
		}
		patchID, err := patchIDBetween(sctx, newBase, newHead)
		if err != nil {
			t.Fatal(err)
		}
		proof := &pipeline.TrivialRebaseAttestation{OldHeadSHA: oldHead, NewHeadSHA: newHead, OldBaseSHA: oldBase, NewBaseSHA: newBase, PatchID: patchID}
		if gap := verifyTrivialRebaseAttestation(sctx, proof); gap != "" {
			t.Fatalf("valid attestation rejected: %s", gap)
		}
		proof.PatchID = strings.Repeat("0", 40)
		if gap := verifyTrivialRebaseAttestation(sctx, proof); gap == "" {
			t.Fatal("forged patch identity passed recomputation")
		}
	})

	t.Run("changed patch", func(t *testing.T) {
		sctx, _ := behindBaseContext(t, &mockAgent{name: "test"}, "MERGEABLE BEHIND")
		oldBase, oldHead := sctx.Run.BaseSHA, sctx.Run.HeadSHA
		newBase := advanceBaseAndRebase(t, sctx)
		if err := os.WriteFile(filepath.Join(sctx.WorkDir, "feature.txt"), []byte("changed after rebase\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitCmd(t, sctx.WorkDir, "add", "feature.txt")
		gitCmd(t, sctx.WorkDir, "commit", "--amend", "--no-edit")
		newHead := gitCmd(t, sctx.WorkDir, "rev-parse", "HEAD")
		if gap := trivialRebasePatchIDGap(sctx, oldBase, oldHead, newBase, newHead); gap == "" {
			t.Fatal("changed patch passed the trivial-rebase proof")
		}
	})
}

func TestCIStep_ReattestsAnUnchangedBehindRebaseWithoutRestart(t *testing.T) {
	sctx, _ := behindBaseContext(t, &mockAgent{name: "test"}, "MERGEABLE BEHIND")
	oldHead, oldBase := sctx.Run.HeadSHA, sctx.Run.BaseSHA
	newBase := advanceBaseOnly(t, sctx)
	bodyFile := filepath.Join(t.TempDir(), "pr-body.md")
	if err := os.WriteFile(bodyFile, []byte(compliantPipelineBody(t, oldHead)), 0o644); err != nil {
		t.Fatal(err)
	}
	prURL := "https://github.com/tmp/fm-no-mistakes-trivial-rebase-reattest-r1/pull/42"
	sctx.Run.PRURL = &prURL
	sctx.Env = append(sctx.Env,
		`FAKE_CLI_PR_LIST_JSON=[{"number":42,"url":"https://github.com/tmp/fm-no-mistakes-trivial-rebase-reattest-r1/pull/42","baseRefName":"main"}]`,
		"FAKE_CLI_PR_BODY_FILE="+bodyFile,
	)
	host, reason := buildHost(sctx, scm.ProviderGitHub)
	if host == nil {
		t.Fatalf("buildHost: %s", reason)
	}
	step := &CIStep{}
	outcome, handled, err := step.tryTrivialBehindRebase(sctx, host, &scm.PR{Number: "42", URL: prURL, BaseBranch: "main"}, newBase)
	if err != nil || !handled || outcome != nil {
		t.Fatalf("tryTrivialBehindRebase() = (%+v, %v, %v), want handled success", outcome, handled, err)
	}
	newHead := gitCmd(t, sctx.WorkDir, "rev-parse", "HEAD")
	remoteHead := strings.Fields(gitCmd(t, sctx.WorkDir, "ls-remote", "origin", "refs/heads/feature"))[0]
	if remoteHead != newHead || newHead == oldHead {
		t.Fatalf("published head = %s, local head = %s, old head = %s", remoteHead, newHead, oldHead)
	}
	attested := parsePipelineAttestationForTest(t, string(mustReadFile(t, bodyFile)))
	if attested.HeadSHA != newHead || attested.TrivialRebase == nil || attested.TrivialRebase.OldHeadSHA != oldHead || attested.TrivialRebase.OldBaseSHA != oldBase || attested.TrivialRebase.NewBaseSHA != newBase {
		t.Fatalf("attestation did not record the verified transition: %+v", attested)
	}
	pending, err := sctx.DB.RunTestRestartPending(sctx.Run.ID)
	if err != nil || pending {
		t.Fatalf("test restart pending = %v, err = %v; trivial rebase must not restart the pipeline", pending, err)
	}
	if got, output := runVerifyPy(t, string(mustReadFile(t, bodyFile)), newHead); got != "success" {
		t.Fatalf("re-attested body rejected by require-no-mistakes: %s\n%s", got, output)
	}
}

func TestCIStep_ConflictingBehindRebaseFallsBackWithoutMutation(t *testing.T) {
	sctx, _ := behindBaseContext(t, &mockAgent{name: "test"}, "MERGEABLE BEHIND")
	oldHead := sctx.Run.HeadSHA
	other := t.TempDir()
	gitCmd(t, other, "clone", "-b", "main", sctx.Repo.UpstreamURL, ".")
	gitCmd(t, other, "config", "user.name", "test")
	gitCmd(t, other, "config", "user.email", "test@test.com")
	if err := os.WriteFile(filepath.Join(other, "feature.txt"), []byte("base conflict\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, other, "add", "feature.txt")
	gitCmd(t, other, "commit", "-m", "conflicting base update")
	newBase := gitCmd(t, other, "rev-parse", "HEAD")
	gitCmd(t, other, "push", "origin", "main")
	gitCmd(t, sctx.WorkDir, "fetch", "origin", "main")
	bodyFile := filepath.Join(t.TempDir(), "pr-body.md")
	if err := os.WriteFile(bodyFile, []byte(compliantPipelineBody(t, oldHead)), 0o644); err != nil {
		t.Fatal(err)
	}
	prURL := "https://github.com/tmp/fm-no-mistakes-trivial-rebase-reattest-r1/pull/42"
	sctx.Run.PRURL = &prURL
	sctx.Env = append(sctx.Env,
		`FAKE_CLI_PR_LIST_JSON=[{"number":42,"url":"https://github.com/tmp/fm-no-mistakes-trivial-rebase-reattest-r1/pull/42","baseRefName":"main"}]`,
		"FAKE_CLI_PR_BODY_FILE="+bodyFile,
	)
	host, reason := buildHost(sctx, scm.ProviderGitHub)
	if host == nil {
		t.Fatalf("buildHost: %s", reason)
	}
	outcome, handled, err := (&CIStep{}).tryTrivialBehindRebase(sctx, host, &scm.PR{Number: "42", URL: prURL, BaseBranch: "main"}, newBase)
	if err != nil || handled || outcome != nil {
		t.Fatalf("conflicted rebase = (%+v, %v, %v), want untouched fallback", outcome, handled, err)
	}
	if head := gitCmd(t, sctx.WorkDir, "rev-parse", "HEAD"); head != oldHead {
		t.Fatalf("failed rebase left HEAD at %s, want original %s", head, oldHead)
	}
	if dirty := gitCmd(t, sctx.WorkDir, "status", "--porcelain"); dirty != "" {
		t.Fatalf("failed rebase left worktree dirty: %s", dirty)
	}
}

func advanceBaseOnly(t *testing.T, sctx *pipeline.StepContext) string {
	t.Helper()
	other := t.TempDir()
	gitCmd(t, other, "clone", "-b", "main", sctx.Repo.UpstreamURL, ".")
	gitCmd(t, other, "config", "user.name", "test")
	gitCmd(t, other, "config", "user.email", "test@test.com")
	if err := os.WriteFile(filepath.Join(other, "base-advance.txt"), []byte("advance\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, other, "add", "base-advance.txt")
	gitCmd(t, other, "commit", "-m", "advance base")
	newBase := gitCmd(t, other, "rev-parse", "HEAD")
	gitCmd(t, other, "push", "origin", "main")
	gitCmd(t, sctx.WorkDir, "fetch", "origin", "main")
	return newBase
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func advanceBaseAndRebase(t *testing.T, sctx *pipeline.StepContext) string {
	t.Helper()
	newBase := advanceBaseOnly(t, sctx)
	gitCmd(t, sctx.WorkDir, "rebase", newBase)
	return newBase
}
