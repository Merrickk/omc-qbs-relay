package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const (
	quickAppVersion = "1.2.4660"
	quickDiscovery  = "https://quick.aws.com/sn"
	quickDataPlane  = "https://cell001.dp.appintegrations.us-east-1.prod.plato.ai.aws.dev"
	quickScope      = "quicksight:read_write"
)

func b64url(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func randToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b64url(b)
}

func pkcePair() (verifier, challenge string) {
	verifier = randToken(32)
	h := sha256.Sum256([]byte(verifier))
	challenge = b64url(h[:])
	return
}

func decodeJWT(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	seg := parts[1]
	seg = strings.ReplaceAll(seg, "-", "+")
	seg = strings.ReplaceAll(seg, "_", "/")
	for len(seg)%4 != 0 {
		seg += "="
	}
	b, err := base64.StdEncoding.DecodeString(seg)
	if err != nil {
		return nil
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

type awsOAuthFlow struct {
	State        string
	Nonce        string
	Verifier     string
	Challenge    string
	Port         int
	RedirectURI  string
	IssuerURL    string
	TenantURL    string
	IDCRegion    string
	ClientID     string
	ClientSecret string
	ExpiresAt    time.Time
}

var (
	awsFlowsMu sync.RWMutex
	awsFlows   = make(map[string]*awsOAuthFlow)
)

func saveAWSFlow(f *awsOAuthFlow) {
	awsFlowsMu.Lock()
	defer awsFlowsMu.Unlock()
	awsFlows[f.State] = f
}

func getAWSFlow(state string) *awsOAuthFlow {
	awsFlowsMu.RLock()
	defer awsFlowsMu.RUnlock()
	if f, ok := awsFlows[state]; ok {
		return f
	}
	// Fallback to latest active flow if state not matched exactly
	var latest *awsOAuthFlow
	for _, f := range awsFlows {
		if latest == nil || f.ExpiresAt.After(latest.ExpiresAt) {
			latest = f
		}
	}
	return latest
}

func findAWSFlowByNonce(nonce string) *awsOAuthFlow {
	awsFlowsMu.RLock()
	defer awsFlowsMu.RUnlock()
	for _, f := range awsFlows {
		if f.Nonce == nonce {
			return f
		}
	}
	return nil
}

// buildDirectAuthorizeURL creates the real AWS Identity Center sign-in URL:
// https://oidc.<region>.amazonaws.com/authorize?response_type=code&client_id=...&redirect_uri=...&scope=quicksight:read_write&state=...
func buildDirectAuthorizeURL(sender httpSender, state string, port int) (string, *awsOAuthFlow, error) {
	cfg := getConfig()
	if port <= 0 {
		port = cfg.OAuthPort
		if port <= 0 {
			port = 1455
		}
	}
	issuerURL := cfg.DefaultIssuer
	if issuerURL == "" {
		issuerURL = "https://identitycenter.amazonaws.com/ssoins-7223d601da4d5e7d"
	}
	tenantURL := cfg.DefaultTenant
	if tenantURL == "" {
		tenantURL = "https://qbs-cell61ad6946-431f-42b4-9209-b8f75fbe7abc.dp.appintegrations.us-east-1.prod.plato.ai.aws.dev"
	}
	idcRegion := cfg.DefaultRegion
	if idcRegion == "" {
		idcRegion = "us-east-1"
	}

	redirectURI := fmt.Sprintf("http://127.0.0.1:%d", port)
	regURL := fmt.Sprintf("https://oidc.%s.amazonaws.com/client/register", idcRegion)
	regBody := map[string]any{
		"clientName":   "AmazonQuick-Desktop",
		"clientType":   "public",
		"scopes":       []string{quickScope},
		"grantTypes":   []string{"authorization_code", "refresh_token"},
		"redirectUris": []string{redirectURI},
		"issuerUrl":    issuerURL,
	}

	regResp, errReg := postJSONTo(sender, regURL, regBody)
	if errReg != nil {
		return "", nil, errReg
	}
	clientID, _ := regResp["clientId"].(string)
	if clientID == "" {
		clientID, _ = regResp["client_id"].(string)
	}
	clientSecret, _ := regResp["clientSecret"].(string)
	if clientSecret == "" {
		clientSecret, _ = regResp["client_secret"].(string)
	}
	if clientID == "" {
		return "", nil, fmt.Errorf("no clientId returned from OIDC client registration")
	}

	verifier, challenge := pkcePair()
	flow := &awsOAuthFlow{
		State:        state,
		Verifier:     verifier,
		Challenge:    challenge,
		Port:         port,
		RedirectURI:  redirectURI,
		IssuerURL:    issuerURL,
		TenantURL:    tenantURL,
		IDCRegion:    idcRegion,
		ClientID:     clientID,
		ClientSecret: clientSecret,
		ExpiresAt:    time.Now().Add(15 * time.Minute),
	}
	saveAWSFlow(flow)

	authorizeURL := fmt.Sprintf("https://oidc.%s.amazonaws.com/authorize?response_type=code&client_id=%s&redirect_uri=%s&scope=%s&state=%s&code_challenge=%s&code_challenge_method=S256",
		idcRegion,
		url.QueryEscape(clientID),
		url.QueryEscape(redirectURI),
		url.QueryEscape(quickScope),
		url.QueryEscape(state),
		url.QueryEscape(challenge),
	)
	return authorizeURL, flow, nil
}

// buildAWSDiscoveryURL fallback for discovery flow
func buildAWSDiscoveryURL(state string, port int) (string, *awsOAuthFlow) {
	if port <= 0 {
		port = 1455
	}
	nonce := randToken(16)
	verifier, challenge := pkcePair()
	flow := &awsOAuthFlow{
		State:       state,
		Nonce:       nonce,
		Verifier:    verifier,
		Challenge:   challenge,
		Port:        port,
		RedirectURI: fmt.Sprintf("http://127.0.0.1:%d", port),
		IDCRegion:   "us-east-1",
		ExpiresAt:   time.Now().Add(15 * time.Minute),
	}
	saveAWSFlow(flow)

	surface := url.QueryEscape(fmt.Sprintf("desktop:%s:%d:%s", nonce, port, quickAppVersion))
	authURL := fmt.Sprintf("%s?qwSurface=%s", quickDiscovery, surface)
	return authURL, flow
}

// handleAWSCallback handles any paste into OMC callback box:
// 1. Raw Session JSON directly
// 2. Stage 1 discovery callback: http://127.0.0.1:<port>/callback?nonce=...&issuerUrl=...&tenantUrl=...&idcRegion=...
// 3. Stage 2 authorization callback: http://127.0.0.1:<port>/?code=...&state=...
// 4. Combined code or URL containing code/state
func handleAWSCallback(sender httpSender, state, rawInput string) (credential, string, error) {
	rawInput = strings.TrimSpace(rawInput)
	if rawInput == "" {
		return credential{}, "", errors.New("empty callback input")
	}

	// 1. If user pasted Session JSON directly
	if strings.HasPrefix(rawInput, "{") && strings.HasSuffix(rawInput, "}") {
		c, err := readCredential([]byte(rawInput))
		if err == nil {
			return c, "", nil
		}
	}

	// 2. Parse as URL
	u, err := url.Parse(rawInput)
	if err != nil {
		return credential{}, "", fmt.Errorf("invalid URL: %w", err)
	}

	q := u.Query()
	flowState := strings.TrimSpace(q.Get("state"))
	if flowState == "" {
		flowState = state
	}
	flow := getAWSFlow(flowState)

	// Case A: Stage 1 discovery callback (/callback?nonce=...&issuerUrl=...&tenantUrl=...&idcRegion=...)
	if strings.HasSuffix(u.Path, "/callback") || q.Get("issuerUrl") != "" || q.Get("issuer_url") != "" {
		nonce := q.Get("nonce")
		if flow == nil && nonce != "" {
			flow = findAWSFlowByNonce(nonce)
		}
		if flow == nil {
			flow = getAWSFlow("")
		}
		if flow == nil {
			return credential{}, "", errors.New("flow not found or expired; please restart login")
		}

		issuerURL := firstVal(q.Get("issuerUrl"), q.Get("issuer_url"), q.Get("issuer"))
		tenantURL := firstVal(q.Get("tenantUrl"), q.Get("tenant_url"))
		idcRegion := firstVal(q.Get("idcRegion"), q.Get("idc_region"), "us-east-1")

		if issuerURL == "" {
			return credential{}, "", errors.New("discovery callback missing issuerUrl")
		}
		flow.IssuerURL = issuerURL
		flow.TenantURL = tenantURL
		flow.IDCRegion = idcRegion

		// Register OIDC client at https://oidc.<idcRegion>.amazonaws.com/client/register
		regURL := fmt.Sprintf("https://oidc.%s.amazonaws.com/client/register", idcRegion)
		regBody := map[string]any{
			"clientName":   "AmazonQuick-Desktop",
			"clientType":   "public",
			"scopes":       []string{quickScope},
			"grantTypes":   []string{"authorization_code", "refresh_token"},
			"redirectUris": []string{flow.RedirectURI},
			"issuerUrl":    issuerURL,
		}
		regResp, errReg := postJSONTo(sender, regURL, regBody)
		if errReg != nil {
			return credential{}, "", fmt.Errorf("client registration failed: %w", errReg)
		}
		clientID, _ := regResp["clientId"].(string)
		if clientID == "" {
			clientID, _ = regResp["client_id"].(string)
		}
		clientSecret, _ := regResp["clientSecret"].(string)
		if clientSecret == "" {
			clientSecret, _ = regResp["client_secret"].(string)
		}
		if clientID == "" {
			return credential{}, "", errors.New("client registration returned no clientId")
		}
		flow.ClientID = clientID
		flow.ClientSecret = clientSecret

		// Build Stage 2 Authorize URL
		authorizeURL := fmt.Sprintf("https://oidc.%s.amazonaws.com/authorize?response_type=code&client_id=%s&redirect_uri=%s&scope=%s&state=%s&code_challenge=%s&code_challenge_method=S256",
			idcRegion,
			url.QueryEscape(clientID),
			url.QueryEscape(flow.RedirectURI),
			url.QueryEscape(quickScope),
			url.QueryEscape(flow.State),
			url.QueryEscape(flow.Challenge),
		)
		// Return authorize URL as next step!
		return credential{}, authorizeURL, nil
	}

	// Case B: Stage 2 code exchange (/ ?code=...&state=...)
	code := q.Get("code")
	if code == "" && !q.Has("state") {
		// If user pasted just a bare code string
		code = rawInput
	}
	if code == "" {
		return credential{}, "", errors.New("callback missing code")
	}

	if flow == nil {
		return credential{}, "", errors.New("OAuth state expired or not found; please restart login")
	}
	if flow.ClientID == "" {
		return credential{}, "", errors.New("OIDC client not yet registered; please complete the authorization step")
	}

	// Exchange code for token
	tokenURL := fmt.Sprintf("https://oidc.%s.amazonaws.com/token", flow.IDCRegion)
	tokPayload := map[string]any{
		"clientId":     flow.ClientID,
		"clientSecret": flow.ClientSecret,
		"grantType":    "authorization_code",
		"code":         code,
		"redirectUri":  flow.RedirectURI,
		"codeVerifier": flow.Verifier,
	}
	tokResp, errTok := postJSONTo(sender, tokenURL, tokPayload)
	if errTok != nil {
		return credential{}, "", fmt.Errorf("token exchange failed: %w", errTok)
	}

	accessToken, _ := tokResp["accessToken"].(string)
	if accessToken == "" {
		accessToken, _ = tokResp["access_token"].(string)
	}
	idToken, _ := tokResp["idToken"].(string)
	if idToken == "" {
		idToken, _ = tokResp["id_token"].(string)
	}
	jwt := idToken
	if jwt == "" {
		jwt = accessToken
	}
	if jwt == "" {
		return credential{}, "", errors.New("token response missing idToken/accessToken")
	}

	// Fetch user-info
	base := strings.TrimRight(flow.TenantURL, "/")
	if base == "" || !strings.Contains(base, "dp.appintegrations") {
		base = quickDataPlane
	}
	userInfoURL := base + "/integration/quick-work/user-info"
	userHeader := http.Header{
		"Authorization": []string{"Bearer " + accessToken},
		"Content-Type":  []string{"application/json"},
		"Accept":        []string{"application/json"},
	}
	userRespRaw, _ := sender.Do(pluginapi.HTTPRequest{
		Method:  http.MethodPost,
		URL:     userInfoURL,
		Headers: userHeader,
		Body:    []byte("{}"),
	})
	var userObj map[string]any
	_ = json.Unmarshal(userRespRaw.Body, &userObj)
	if sub, ok := userObj["userInfo"].(map[string]any); ok {
		userObj = sub
	}

	pickField := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := userObj[k]; ok {
				if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
					return strings.TrimSpace(s)
				}
			}
		}
		return ""
	}

	claims := decodeJWT(jwt)
	accountID := pickField("awsAccountId", "aws_account_id", "accountId")
	if accountID == "" && claims != nil {
		if v, ok := claims["aws_account_id"].(string); ok {
			accountID = v
		}
	}
	email := pickField("email", "emailAddress")
	if email == "" && claims != nil {
		if v, ok := claims["email"].(string); ok {
			email = v
		}
	}

	tenant := flow.TenantURL
	if tenant == "" {
		tenant = quickDataPlane
	}
	if !strings.HasPrefix(tenant, "http://") && !strings.HasPrefix(tenant, "https://") {
		tenant = "https://" + tenant
	}
	uTenant, _ := url.Parse(tenant)
	wsHost := uTenant.Host

	refreshToken, _ := tokResp["refreshToken"].(string)
	if refreshToken == "" {
		refreshToken, _ = tokResp["refresh_token"].(string)
	}

	c := credential{
		Type:         credentialProvider,
		AccountID:    accountID,
		JWT:          jwt,
		WSHost:       wsHost,
		TenantURL:    tenant,
		Email:        email,
		Region:       flow.IDCRegion,
		UsableUntil:  float64(time.Now().Add(42 * time.Minute).Unix()),
		RefreshToken: refreshToken,
		ClientID:     flow.ClientID,
		ClientSecret: flow.ClientSecret,
		IssuerURL:    flow.IssuerURL,
	}

	return c, "", nil
}

func firstVal(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func postJSONTo(sender httpSender, url string, body any) (map[string]any, error) {
	b, _ := json.Marshal(body)
	resp, err := sender.Do(pluginapi.HTTPRequest{
		Method:  http.MethodPost,
		URL:     url,
		Headers: http.Header{"Content-Type": {"application/json"}, "Accept": {"application/json"}},
		Body:    b,
	})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(resp.Body))
	}
	var out map[string]any
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return nil, fmt.Errorf("invalid JSON response: %w", err)
	}
	return out, nil
}

func refreshAWSAuth(sender httpSender, c credential) (credential, error) {
	if c.RefreshToken == "" {
		return c, errors.New("no refresh_token in credential; please re-login")
	}
	region := c.Region
	if region == "" {
		region = "us-east-1"
	}
	tokenURL := fmt.Sprintf("https://oidc.%s.amazonaws.com/token", region)
	tokPayload := map[string]any{
		"clientId":     c.ClientID,
		"clientSecret": c.ClientSecret,
		"grantType":    "refresh_token",
		"refreshToken": c.RefreshToken,
	}
	tokResp, err := postJSONTo(sender, tokenURL, tokPayload)
	if err != nil {
		return c, fmt.Errorf("token refresh failed: %w", err)
	}
	accessToken, _ := tokResp["accessToken"].(string)
	if accessToken == "" {
		accessToken, _ = tokResp["access_token"].(string)
	}
	idToken, _ := tokResp["idToken"].(string)
	if idToken == "" {
		idToken, _ = tokResp["id_token"].(string)
	}
	jwt := idToken
	if jwt == "" {
		jwt = accessToken
	}
	if jwt != "" {
		c.JWT = jwt
	}
	if newRt, ok := tokResp["refreshToken"].(string); ok && newRt != "" {
		c.RefreshToken = newRt
	} else if newRt, ok := tokResp["refresh_token"].(string); ok && newRt != "" {
		c.RefreshToken = newRt
	}
	c.UsableUntil = float64(time.Now().Add(42 * time.Minute).Unix())
	return c, nil
}
