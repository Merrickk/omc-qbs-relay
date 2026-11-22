package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const credentialProvider = "qbs-relay"

type credential struct {
	Type         string  `json:"type"`
	AccountID    string  `json:"account_id"`
	JWT          string  `json:"jwt"`
	WSHost       string  `json:"ws_host"`
	OutpostID    string  `json:"outpost_id,omitempty"`
	UsableUntil  float64 `json:"usable_until,omitempty"`
	Email        string  `json:"email,omitempty"`
	Label        string  `json:"label,omitempty"`
	TenantURL    string  `json:"tenant_url,omitempty"`
	Region       string  `json:"region,omitempty"`
	ProviderName string  `json:"provider,omitempty"`
	RefreshToken string  `json:"refresh_token,omitempty"`
	ClientID     string  `json:"client_id,omitempty"`
	ClientSecret string  `json:"client_secret,omitempty"`
	IssuerURL    string  `json:"issuer_url,omitempty"`
}

func readCredential(raw []byte) (credential, error) {
	var obj map[string]any
	if json.Unmarshal(raw, &obj) != nil {
		return credential{}, errors.New("invalid credential JSON")
	}
	pick := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := obj[k]; ok {
				if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
					return strings.TrimSpace(s)
				}
			}
		}
		return ""
	}
	c := credential{
		Type:         pick("type"),
		AccountID:    pick("account_id", "aws_account_id", "awsAccountId"),
		JWT:          pick("jwt", "id_token", "access_token"),
		WSHost:       pick("ws_host", "wsHost"),
		OutpostID:    pick("outpost_id", "outpostId"),
		Email:        pick("email", "emailAddress", "display_name", "displayName"),
		Label:        pick("label", "user_name", "userName"),
		TenantURL:    pick("tenant_url", "tenantUrl"),
		Region:       pick("region"),
		ProviderName: pick("provider"),
		RefreshToken: pick("refresh_token", "refreshToken"),
		ClientID:     pick("client_id", "clientId"),
		ClientSecret: pick("client_secret", "clientSecret"),
		IssuerURL:    pick("issuer_url", "issuerUrl"),
	}
	if tokObj, ok := obj["tokens"].(map[string]any); ok {
		if c.RefreshToken == "" {
			if rt, ok := tokObj["refresh_token"].(string); ok {
				c.RefreshToken = rt
			}
		}
	}
	if clientObj, ok := obj["client"].(map[string]any); ok {
		if c.ClientID == "" {
			if cid, ok := clientObj["client_id"].(string); ok {
				c.ClientID = cid
			}
		}
		if c.ClientSecret == "" {
			if cs, ok := clientObj["client_secret"].(string); ok {
				c.ClientSecret = cs
			}
		}
		if c.IssuerURL == "" {
			if iu, ok := clientObj["issuer_url"].(string); ok {
				c.IssuerURL = iu
			}
		}
	}
	if c.UsableUntil == 0 {
		for _, k := range []string{"usable_until", "usableUntil", "access_token_expiry"} {
			if v, ok := obj[k]; ok {
				switch n := v.(type) {
				case float64:
					c.UsableUntil = n
				case int:
					c.UsableUntil = float64(n)
				case string:
					var f float64
					if json.Unmarshal([]byte(n), &f) == nil {
						c.UsableUntil = f
					}
				}
				if c.UsableUntil != 0 {
					break
				}
			}
		}
	}
	if c.WSHost == "" && c.TenantURL != "" {
		if u, err := url.Parse(c.TenantURL); err == nil {
			c.WSHost = u.Host
		}
	}
	c.Type = strings.ToLower(strings.TrimSpace(c.Type))
	if c.Type != "" && c.Type != credentialProvider && c.Type != "aws-quick" {
		return c, errors.New("not a qbs-relay credential")
	}
	c.Type = credentialProvider
	if c.AccountID == "" || c.JWT == "" || c.WSHost == "" {
		return c, errors.New("account_id, jwt and ws_host are required")
	}
	return c, nil
}

func (c credential) jsonBytes() []byte {
	if strings.TrimSpace(c.Type) == "" {
		c.Type = credentialProvider
	}
	b, _ := json.Marshal(c)
	return b
}

func (c credential) authID() string {
	sum := sha256.Sum256([]byte(c.AccountID))
	return "qbs-" + hex.EncodeToString(sum[:12]) + ".json"
}

func (c credential) displayName() string {
	if strings.TrimSpace(c.Email) != "" {
		return c.Email
	}
	if strings.TrimSpace(c.Label) != "" {
		return c.Label
	}
	return c.AccountID
}

func authDataFor(c credential) pluginapi.AuthData {
	return pluginapi.AuthData{
		Provider:    credentialProvider,
		ID:          c.authID(),
		FileName:    c.authID(),
		Label:       c.displayName(),
		StorageJSON: c.jsonBytes(),
		Metadata: map[string]any{
			"type":       credentialProvider,
			"account_id": c.AccountID,
			"email":      c.Email,
		},
	}
}
