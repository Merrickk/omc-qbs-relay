package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestQuotaFetchMock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/integration/quick-work/user-info" {
			w.Write([]byte(`{
				"email": "test@example.com",
				"role": "EXPLORER",
				"usageSummary": {
					"monthlyUsage": {
						"availableUnits": 598,
						"provisionedUnits": 600,
						"usedPercentage": 0.33,
						"resetsAt": 1796083200
					},
					"sessionUsage": {
						"usedPercentage": 10,
						"resumeInMinutes": 0
					},
					"subscription": {
						"subscriptionType": "QUICK_LITE_FREE_TRIAL"
					}
				}
			}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	cred := credential{
		Type:      credentialProvider,
		AccountID: "123456",
		JWT:       "test-jwt",
		TenantURL: srv.URL,
	}

	sender := directTransportSender{client: srv.Client()}
	resp, err := quotaFetch(pluginapi.QuotaFetchRequest{
		StorageJSON: cred.jsonBytes(),
	}, sender)

	if err != nil {
		t.Fatalf("quotaFetch failed: %v", err)
	}

	if resp.Subscription == nil || resp.Subscription.Plan != "QUICK_LITE_FREE_TRIAL" {
		t.Fatalf("unexpected subscription: %+v", resp.Subscription)
	}
	if len(resp.Groups) == 0 || len(resp.Groups[0].Buckets) < 2 {
		t.Fatalf("unexpected buckets: %+v", resp.Groups)
	}

	b1 := resp.Groups[0].Buckets[0]
	if b1.Window != "Monthly" || b1.RemainingFraction < 0.99 {
		t.Fatalf("unexpected monthly bucket: %+v", b1)
	}
}
