package azuredevops

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/scm"
)

const azureReviewHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const azureReviewBotID = "00000000-1111-2222-3333-444444444444"

func TestRequiredReviewStatusIsBoundToHeadAndIdentity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, state, description, botID string
		wantBucket                      scm.CheckBucket
	}{
		{"completed", "succeeded", "Review completed", azureReviewBotID, scm.CheckBucketPass},
		{"approved", "succeeded", "Review approved", azureReviewBotID, scm.CheckBucketPass},
		{"missing", "", "", "", scm.CheckBucketPending},
		{"pending", "pending", "Review in progress", azureReviewBotID, scm.CheckBucketPending},
		{"failed", "failed", "Review failed", azureReviewBotID, scm.CheckBucketFail},
		{"error", "error", "Review failed", azureReviewBotID, scm.CheckBucketFail},
		{"rate limited", "succeeded", "Review rate limited", azureReviewBotID, scm.CheckBucketFail},
		{"wrong identity", "succeeded", "Review completed", "another-user", scm.CheckBucketFail},
		{"unknown state", "future", "Review completed", azureReviewBotID, scm.CheckBucketFail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status := `"count":0,"value":[]`
			if tc.state != "" {
				status = fmt.Sprintf(`"count":1,"value":[{"id":17,"state":%q,"description":%q,"context":{"genre":"code review","name":"CodeRabbit"},"createdBy":{"id":%q}}]`, tc.state, tc.description, tc.botID)
			}
			threadsCommand := azureReviewThreadsCommand()
			responses := map[string]azdoTestResponse{
				threadsCommand: {stdout: `{"count":0,"value":[]}`},
				"az repos pr policy list --id 42 --organization " + testOrg + " --output json": {stdout: "[]"},
				"az repos pr show --id 42 --organization " + testOrg + " --output json":        {stdout: azureReviewPR(azureReviewHead)},
				azureReviewStatusCommand(azureReviewHead):                                      {stdout: "{" + status + "}"},
			}
			host := newTestHost(responses).WithRequiredReviews([]string{"coderabbit"}, azureReviewBotID)
			pr := &scm.PR{Number: "42", HeadSHA: azureReviewHead}
			checks, err := host.GetChecks(context.Background(), pr)
			if err != nil {
				t.Fatal(err)
			}
			if len(checks) != 1 || checks[0].ExternalReview != "coderabbit" || checks[0].Bucket != tc.wantBucket || checks[0].HeadSHA != azureReviewHead || pr.ObservedHeadSHA != azureReviewHead {
				t.Fatalf("checks=%+v observed=%s", checks, pr.ObservedHeadSHA)
			}
		})
	}
}

func TestRequiredReviewReadsOnlyUnresolvedThreadsFromConfiguredIdentity(t *testing.T) {
	t.Parallel()
	threadsCommand := azureReviewThreadsCommand()
	responses := map[string]azdoTestResponse{
		"az repos pr show --id 42 --organization " + testOrg + " --output json": {stdout: azureReviewPR(azureReviewHead)},
		threadsCommand: {stdout: `{"count":2,"value":[{"id":1,"status":"active","threadContext":{"filePath":"src/a.go","rightFileStart":{"line":8}},"comments":[{"id":10,"content":"untrusted text","author":{"id":"` + azureReviewBotID + `","displayName":"CodeRabbit"}}]},{"id":2,"status":"fixed","comments":[{"id":11,"content":"resolved","author":{"id":"` + azureReviewBotID + `"}}]}]}`},
	}
	host := newTestHost(responses).WithRequiredReviews([]string{"coderabbit"}, azureReviewBotID)
	if !host.Capabilities().ReviewComments {
		t.Fatal("Azure DevOps must expose review comments")
	}
	comments, err := host.GetReviewComments(context.Background(), &scm.PR{Number: "42"})
	if err != nil || len(comments) != 1 || comments[0].Path != "src/a.go" || comments[0].Line != 8 || !comments[0].VerifiedAuthor {
		t.Fatalf("comments=%+v, err=%v", comments, err)
	}
}

func TestAzureReviewThreadStatusNames(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		status string
		want   int
	}{
		{"active", 1}, {"pending", 1}, {"fixed", 0}, {"wontFix", 0}, {"closed", 0}, {"byDesign", 0}, {"future", 1},
	} {
		t.Run(tc.status, func(t *testing.T) {
			threadsCommand := azureReviewThreadsCommand()
			body := fmt.Sprintf(`{"count":1,"value":[{"id":1,"status":%q,"threadContext":{"filePath":"a.go","rightFileStart":{"line":3}},"comments":[{"id":2,"content":"x","author":{"id":%q}}]}]}`, tc.status, azureReviewBotID)
			host := newTestHost(map[string]azdoTestResponse{
				"az repos pr show --id 42 --organization " + testOrg + " --output json": {stdout: azureReviewPR(azureReviewHead)},
				threadsCommand: {stdout: body},
			}).WithRequiredReviews([]string{"coderabbit"}, azureReviewBotID)
			comments, err := host.GetReviewComments(context.Background(), &scm.PR{Number: "42"})
			if err != nil || len(comments) != tc.want {
				t.Fatalf("status %q comments=%+v err=%v", tc.status, comments, err)
			}
		})
	}
}

func TestRequiredReviewStatusHeadMovementFailsClosed(t *testing.T) {
	t.Parallel()
	responses := map[string]azdoTestResponse{
		"az repos pr policy list --id 42 --organization " + testOrg + " --output json": {stdout: "[]"},
		"az repos pr show --id 42 --organization " + testOrg + " --output json":        {stdout: azureReviewPR(azureReviewHead)},
		azureReviewStatusCommand(azureReviewHead):                                      {stdout: `{"count":1,"value":[{"id":17,"state":"succeeded","description":"Review completed","context":{"genre":"code review","name":"CodeRabbit"},"createdBy":{"id":"` + azureReviewBotID + `"}}]}`},
	}
	count := 0
	host := New(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		key := strings.TrimSpace(name + " " + strings.Join(args, " "))
		response := responses[key]
		if strings.HasPrefix(key, "az repos pr show") {
			count++
			if count == 2 {
				response.stdout = azureReviewPR(strings.Repeat("b", 40))
			}
		}
		return azdoTestCmdFactory(map[string]azdoTestResponse{key: response})(ctx, name, args...)
	}, nil, testOrg, testProject, testRepo).WithRequiredReviews([]string{"coderabbit"}, azureReviewBotID)
	_, err := host.GetChecks(context.Background(), &scm.PR{Number: "42"})
	if !errors.Is(err, scm.ErrHeadChanged) {
		t.Fatalf("head movement error=%v", err)
	}
}

func TestRequiredReviewHeadMovementDuringCommentReadFailsClosed(t *testing.T) {
	t.Parallel()
	threadsCommand := azureReviewThreadsCommand()
	responses := map[string]azdoTestResponse{
		"az repos pr policy list --id 42 --organization " + testOrg + " --output json": {stdout: "[]"},
		"az repos pr show --id 42 --organization " + testOrg + " --output json":        {stdout: azureReviewPR(azureReviewHead)},
		azureReviewStatusCommand(azureReviewHead):                                      {stdout: `{"count":1,"value":[{"id":17,"state":"succeeded","description":"Review completed","context":{"genre":"code review","name":"CodeRabbit"},"createdBy":{"id":"` + azureReviewBotID + `"}}]}`},
		threadsCommand: {stdout: `{"count":0,"value":[]}`},
	}
	count := 0
	host := New(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		key := strings.TrimSpace(name + " " + strings.Join(args, " "))
		response := responses[key]
		if strings.HasPrefix(key, "az repos pr show") {
			count++
			if count >= 3 {
				response.stdout = azureReviewPR(strings.Repeat("b", 40))
			}
		}
		return azdoTestCmdFactory(map[string]azdoTestResponse{key: response})(ctx, name, args...)
	}, nil, testOrg, testProject, testRepo).WithRequiredReviews([]string{"coderabbit"}, azureReviewBotID)
	_, err := host.GetChecks(context.Background(), &scm.PR{Number: "42"})
	if !errors.Is(err, scm.ErrHeadChanged) {
		t.Fatalf("head movement during comment read error=%v", err)
	}
}

func TestRequiredReviewAzureDevOpsServerIsUnsupported(t *testing.T) {
	t.Parallel()
	responses := map[string]azdoTestResponse{
		"az repos pr policy list --id 42 --organization https://tfs.example/collection --output json": {stdout: "[]"},
		"az repos pr show --id 42 --organization https://tfs.example/collection --output json":        {stdout: azureReviewPR(azureReviewHead)},
	}
	host := New(azdoTestCmdFactory(responses), func() bool { return true }, "https://tfs.example/collection", testProject, testRepo).WithRequiredReviews([]string{"coderabbit"}, azureReviewBotID)
	checks, err := host.GetChecks(context.Background(), &scm.PR{Number: "42"})
	if err != nil || len(checks) != 1 || !strings.Contains(checks[0].State, "AZURE DEVOPS SERVER") {
		t.Fatalf("server unsupported = %+v, %v", checks, err)
	}
}

func TestRequiredReviewWithoutConfiguredBotIDIsUnsupported(t *testing.T) {
	t.Parallel()
	host := newTestHost(map[string]azdoTestResponse{
		"az repos pr policy list --id 42 --organization " + testOrg + " --output json": {stdout: "[]"},
		"az repos pr show --id 42 --organization " + testOrg + " --output json":        {stdout: azureReviewPR(azureReviewHead)},
	}).WithRequiredReviews([]string{"coderabbit"}, "")
	checks, err := host.GetChecks(context.Background(), &scm.PR{Number: "42"})
	if err != nil || len(checks) != 1 || checks[0].HeadSHA != azureReviewHead || !strings.Contains(checks[0].State, "ci.required_review_bot_id") {
		t.Fatalf("unsupported = %+v, %v", checks, err)
	}
}

func azureReviewPR(head string) string {
	return fmt.Sprintf(`{"pullRequestId":42,"lastMergeSourceCommit":{"commitId":%q}}`, head)
}

func azureReviewThreadsCommand() string {
	return "az devops invoke --organization " + testOrg + " --area git --resource threads --route-parameters project=myproject repositoryId=myrepo pullRequestId=42 --api-version 7.1 --http-method GET --output json"
}

func azureReviewStatusCommand(head string) string {
	return "az devops invoke --organization " + testOrg + " --area git --resource statuses --route-parameters project=myproject repositoryId=myrepo commitId=" + head + " --query-parameters latestOnly=true top=1000 --api-version 7.1 --http-method GET --output json"
}
