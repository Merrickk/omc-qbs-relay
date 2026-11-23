package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

type quickUserUsageResponse struct {
	AccountName  string `json:"accountName"`
	AwsAccountID string `json:"awsAccountId"`
	Email        string `json:"email"`
	Role         string `json:"role"`
	UsageSummary struct {
		EntitlementStatus string `json:"entitlementStatus"`
		MonthlyUsage      struct {
			AvailableUnits   float64 `json:"availableUnits"`
			ProvisionedUnits float64 `json:"provisionedUnits"`
			ResetsAt         int64   `json:"resetsAt"`
			UsedPercentage   float64 `json:"usedPercentage"`
		} `json:"monthlyUsage"`
		SessionUsage struct {
			ResumeInMinutes int     `json:"resumeInMinutes"`
			UsedPercentage  float64 `json:"usedPercentage"`
		} `json:"sessionUsage"`
		Subscription struct {
			SubscriptionType string `json:"subscriptionType"`
			FreeTrialMetadata struct {
				ExpiryDate int64  `json:"expiryDate"`
				Status     string `json:"status"`
			} `json:"freeTrialMetadata"`
		} `json:"subscription"`
	} `json:"usageSummary"`
}

func quotaDescription() pluginapi.QuotaDescribeResponse {
	return pluginapi.QuotaDescribeResponse{
		SupportedProviders: []string{credentialProvider},
		DisplayName:        "AWS Quick",
		SupportsReset:      false,
	}
}

func quotaReset() pluginapi.QuotaResetResponse {
	return pluginapi.QuotaResetResponse{
		Success: false,
		Message: "AWS Quick quotas are managed directly in the AWS Quick console",
	}
}

func quotaFetch(req pluginapi.QuotaFetchRequest, sender httpSender) (pluginapi.QuotaFetchResponse, error) {
	c, err := readCredential(req.StorageJSON)
	if err != nil {
		return pluginapi.QuotaFetchResponse{}, err
	}

	// Auto-refresh token if needed
	if c.RefreshToken != "" && c.ClientID != "" {
		if c.UsableUntil > 0 && time.Now().Unix() > int64(c.UsableUntil)-120 {
			if refreshed, errRef := refreshAWSAuth(sender, c); errRef == nil {
				c = refreshed
			}
		}
	}

	base := strings.TrimRight(c.TenantURL, "/")
	if base == "" {
		base = quickDataPlane
	}
	userInfoURL := base + "/integration/quick-work/user-info"
	userHeader := http.Header{
		"Authorization": []string{"Bearer " + c.JWT},
		"Content-Type":  []string{"application/json"},
		"Accept":        []string{"application/json"},
	}

	rawResp, errReq := sender.Do(pluginapi.HTTPRequest{
		Method:  http.MethodPost,
		URL:     userInfoURL,
		Headers: userHeader,
		Body:    []byte("{}"),
	})
	if errReq != nil {
		return pluginapi.QuotaFetchResponse{}, errReq
	}
	if rawResp.StatusCode == http.StatusUnauthorized && c.RefreshToken != "" {
		// Retry once with refreshed token
		if refreshed, errRef := refreshAWSAuth(sender, c); errRef == nil {
			c = refreshed
			userHeader.Set("Authorization", "Bearer "+c.JWT)
			rawResp, _ = sender.Do(pluginapi.HTTPRequest{
				Method:  http.MethodPost,
				URL:     userInfoURL,
				Headers: userHeader,
				Body:    []byte("{}"),
			})
		}
	}

	var userResp quickUserUsageResponse
	if err := json.Unmarshal(rawResp.Body, &userResp); err != nil {
		return pluginapi.QuotaFetchResponse{}, fmt.Errorf("failed to parse AWS Quick user info: %w", err)
	}

	u := userResp.UsageSummary
	subType := u.Subscription.SubscriptionType
	if subType == "" {
		subType = "Amazon Quick"
	}

	resp := pluginapi.QuotaFetchResponse{
		Subscription: &pluginapi.QuotaSubscription{
			Plan:     subType,
			TierName: userResp.Role,
		},
	}

	// Group: Amazon Quick Usage
	group := pluginapi.QuotaGroup{
		DisplayName: "Amazon Quick",
	}

	// Monthly units bucket
	if u.MonthlyUsage.ProvisionedUnits > 0 {
		remainFrac := u.MonthlyUsage.AvailableUnits / u.MonthlyUsage.ProvisionedUnits
		if remainFrac < 0 {
			remainFrac = 0
		} else if remainFrac > 1 {
			remainFrac = 1
		}
		resetTimeStr := ""
		if u.MonthlyUsage.ResetsAt > 0 {
			resetTimeStr = time.Unix(u.MonthlyUsage.ResetsAt, 0).UTC().Format(time.RFC3339)
		}
		desc := fmt.Sprintf("%.0f / %.0f units available (%.1f%% used)",
			u.MonthlyUsage.AvailableUnits,
			u.MonthlyUsage.ProvisionedUnits,
			u.MonthlyUsage.UsedPercentage,
		)
		group.Buckets = append(group.Buckets, pluginapi.QuotaBucket{
			Window:            "Monthly",
			RemainingFraction: remainFrac,
			ResetTime:         resetTimeStr,
			Description:       desc,
		})
	}

	// Session rate-limit bucket
	sessionFrac := 1.0 - (u.SessionUsage.UsedPercentage / 100.0)
	if sessionFrac < 0 {
		sessionFrac = 0
	} else if sessionFrac > 1 {
		sessionFrac = 1
	}
	sessionDesc := fmt.Sprintf("Session capacity (%.0f%% used)", u.SessionUsage.UsedPercentage)
	if u.SessionUsage.ResumeInMinutes > 0 {
		sessionDesc += fmt.Sprintf(", resume in %d min", u.SessionUsage.ResumeInMinutes)
	}
	group.Buckets = append(group.Buckets, pluginapi.QuotaBucket{
		Window:            "Session",
		RemainingFraction: sessionFrac,
		Description:       sessionDesc,
	})

	resp.Groups = append(resp.Groups, group)

	// Summary metrics
	resp.Summary = []pluginapi.QuotaMetric{
		{
			Key:    "available_units",
			Label:  "Available Units",
			Value:  u.MonthlyUsage.AvailableUnits,
			Unit:   "units",
			Format: "number",
		},
		{
			Key:    "provisioned_units",
			Label:  "Provisioned Units",
			Value:  u.MonthlyUsage.ProvisionedUnits,
			Unit:   "units",
			Format: "number",
		},
	}

	return resp, nil
}
