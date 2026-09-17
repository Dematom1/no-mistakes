package steps

import (
	"fmt"
	"strings"

	"github.com/kunchenguid/no-mistakes/internal/pipeline"
	"github.com/kunchenguid/no-mistakes/internal/scm"
)

func requiresExternalReview(sctx *pipeline.StepContext) bool {
	return sctx.Config != nil && len(sctx.Config.CI.RequiredReviews) != 0
}

// requiredReviewChecks checks that the forge adapter supplied every opted-in
// review requirement. Unsupported forges cannot silently skip a requirement.
func requiredReviewChecks(sctx *pipeline.StepContext, host scm.Host, pr *scm.PR, checks []scm.Check) ([]scm.Check, error) {
	if !requiresExternalReview(sctx) {
		return checks, nil
	}
	for _, reviewer := range sctx.Config.CI.RequiredReviews {
		found := false
		for _, check := range checks {
			if check.ExternalReview != reviewer {
				continue
			}
			found = true
			if check.HeadSHA == "" || check.HeadSHA != pr.HeadSHA {
				return nil, fmt.Errorf("external review %s does not match the current PR head", reviewer)
			}
		}
		if !found {
			checks = append(checks, scm.Check{
				Name: "External review: " + reviewer, ProviderID: "external-review:" + reviewer,
				ExternalReview: reviewer, Bucket: scm.CheckBucketFail,
				State: "UNSUPPORTED ON " + strings.ToUpper(string(host.Provider())),
			})
		}
	}
	return checks, nil
}
