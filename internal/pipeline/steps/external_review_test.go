package steps

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/config"
	"github.com/kunchenguid/no-mistakes/internal/pipeline"
	"github.com/kunchenguid/no-mistakes/internal/scm"
	"github.com/kunchenguid/no-mistakes/internal/types"
)

type reviewProvider struct {
	scm.Host
	provider scm.Provider
}

func (h reviewProvider) Provider() scm.Provider { return h.provider }

func TestRequiredReviewUnsupportedProvidersParkExplicitly(t *testing.T) {
	for _, provider := range []scm.Provider{
		scm.ProviderGitLab, scm.ProviderBitbucket, scm.ProviderGitea, scm.ProviderForgejo,
		scm.ProviderOrigin, scm.PluginProvider("example"),
	} {
		t.Run(string(provider), func(t *testing.T) {
			sctx := &pipeline.StepContext{Config: &config.Config{CI: config.CI{RequiredReviews: []string{"coderabbit"}}}}
			host := reviewProvider{provider: provider}
			checks, err := requiredReviewChecks(sctx, host, &scm.PR{HeadSHA: "current-head"}, nil)
			if err != nil || len(checks) != 1 || checks[0].ExternalReview != "coderabbit" || checks[0].Bucket != scm.CheckBucketFail || checks[0].State != "UNSUPPORTED ON "+strings.ToUpper(string(provider)) {
				t.Fatalf("checks=%+v err=%v", checks, err)
			}
			findings := ciObservationFindings(ciIssues{
				provider: provider, requiredReviews: []string{"coderabbit"}, checks: checks,
				failing: []string{checks[0].Name},
			})
			data, marshalErr := json.Marshal(findings)
			parsed, err := types.ParseFindingsJSON(string(data))
			if marshalErr != nil || err != nil || len(parsed.Items) != 1 || parsed.Items[0].Action != types.ActionAskUser || parsed.Items[0].Category != types.FindingCategoryCIReviewBot {
				t.Fatalf("findings=%+v marshalErr=%v err=%v", parsed, marshalErr, err)
			}
		})
	}
}
