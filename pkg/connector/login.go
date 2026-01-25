package connector

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"go.mau.fi/mautrix-claude/pkg/claudeapi"
)

// APIKeyLogin handles API key-based login.
type APIKeyLogin struct {
	User      *bridgev2.User
	Connector *ClaudeConnector
}

// SessionCookieLogin handles session cookie-based login.
type SessionCookieLogin struct {
	User      *bridgev2.User
	Connector *ClaudeConnector
}

var (
	_ bridgev2.LoginProcess          = (*APIKeyLogin)(nil)
	_ bridgev2.LoginProcessUserInput = (*APIKeyLogin)(nil)
	_ bridgev2.LoginProcess          = (*SessionCookieLogin)(nil)
	_ bridgev2.LoginProcessUserInput = (*SessionCookieLogin)(nil)
)

// Start begins the API key login flow.
func (a *APIKeyLogin) Start(ctx context.Context) (*bridgev2.LoginStep, error) {
	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeUserInput,
		StepID:       "api_key",
		Instructions: "Enter your Claude API key. Get one from: https://console.anthropic.com/settings/keys",
		UserInputParams: &bridgev2.LoginUserInputParams{
			Fields: []bridgev2.LoginInputDataField{
				{
					Type:        bridgev2.LoginInputFieldTypePassword,
					ID:          "api_key",
					Name:        "API Key",
					Description: "Your Claude API key (sk-ant-...)",
				},
			},
		},
	}, nil
}

// SubmitUserInput processes the submitted API key.
func (a *APIKeyLogin) SubmitUserInput(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	apiKey := input["api_key"]

	// Validate API key format
	if !isValidAPIKeyFormat(apiKey) {
		return nil, fmt.Errorf("invalid API key format")
	}

	// Test the API key
	client := claudeapi.NewClient(apiKey, a.Connector.Log)
	if err := client.Validate(ctx); err != nil {
		if claudeapi.IsAuthError(err) {
			return nil, fmt.Errorf("invalid API key: authentication failed")
		}
		return nil, fmt.Errorf("failed to validate API key: %w", err)
	}

	// Create user login with hashed API key (for privacy - no raw key material in ID)
	hash := sha256.Sum256([]byte(apiKey))
	loginID := networkid.UserLoginID(fmt.Sprintf("claude_%s", hex.EncodeToString(hash[:10])))
	userLogin, err := a.User.NewLogin(ctx, &database.UserLogin{
		ID:         loginID,
		RemoteName: "Claude API User",
		Metadata: &UserLoginMetadata{
			AuthType: "api_key",
			APIKey:   apiKey,
		},
	}, nil)
	if err != nil {
		return nil, err
	}

	// Set up client
	claudeClient := &ClaudeClient{
		MessageClient: client,
		UserLogin:     userLogin,
		Connector:     a.Connector,
		conversations: make(map[networkid.PortalID]*claudeapi.ConversationManager),
	}
	userLogin.Client = claudeClient

	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeComplete,
		StepID:       "complete",
		Instructions: "Successfully authenticated with Claude API",
		CompleteParams: &bridgev2.LoginCompleteParams{
			UserLogin: userLogin,
		},
	}, nil
}

// Cancel cancels the login process.
func (a *APIKeyLogin) Cancel() {}

// isValidAPIKeyFormat checks if an API key has a valid format.
func isValidAPIKeyFormat(apiKey string) bool {
	if apiKey == "" {
		return false
	}

	// Claude API keys start with "sk-ant-"
	if !strings.HasPrefix(apiKey, "sk-ant-") {
		return false
	}

	// Must be longer than just the prefix
	if len(apiKey) <= len("sk-ant-") {
		return false
	}

	return true
}

// Start begins the session cookie login flow.
func (s *SessionCookieLogin) Start(ctx context.Context) (*bridgev2.LoginStep, error) {
	return &bridgev2.LoginStep{
		Type:   bridgev2.LoginStepTypeUserInput,
		StepID: "session_cookie",
		Instructions: `Enter your Claude cookies. Due to Cloudflare protection, you need ALL cookies.

How to get cookies:
1. Open claude.ai in Chrome/Firefox
2. Press F12 → Network tab
3. Refresh the page
4. Click any request to claude.ai
5. Find "Cookie:" in Request Headers
6. Copy the ENTIRE cookie string

It should look like: sessionKey=sk-ant-...; cf_clearance=...; __cf_bm=...`,
		UserInputParams: &bridgev2.LoginUserInputParams{
			Fields: []bridgev2.LoginInputDataField{
				{
					Type:        bridgev2.LoginInputFieldTypePassword,
					ID:          "cookies",
					Name:        "All Cookies",
					Description: "Full cookie string from browser (includes sessionKey, cf_clearance, etc.)",
				},
			},
		},
	}, nil
}

// SubmitUserInput processes the submitted session cookie.
func (s *SessionCookieLogin) SubmitUserInput(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	cookies := strings.TrimSpace(input["cookies"])

	// Validate cookies are not empty
	if cookies == "" {
		return nil, fmt.Errorf("cookies cannot be empty")
	}

	// Extract sessionKey from cookies for the client
	sessionKey := extractSessionKey(cookies)
	if sessionKey == "" {
		return nil, fmt.Errorf("sessionKey not found in cookies. Make sure you copied the full cookie string")
	}

	// Create web client with full cookies and validate
	client := claudeapi.NewWebClient(sessionKey, s.Connector.Log)
	client.AllCookies = cookies
	if err := client.Validate(ctx); err != nil {
		return nil, fmt.Errorf("invalid session cookie: %w", err)
	}

	// Create user login with hashed session key (for privacy)
	hash := sha256.Sum256([]byte(sessionKey))
	loginID := networkid.UserLoginID(fmt.Sprintf("claude_web_%s", hex.EncodeToString(hash[:10])))
	userLogin, err := s.User.NewLogin(ctx, &database.UserLogin{
		ID:         loginID,
		RemoteName: "Claude Web User",
		Metadata: &UserLoginMetadata{
			AuthType:       "session_cookie",
			SessionKey:     cookies, // Store full cookies
			OrganizationID: client.OrganizationID,
		},
	}, nil)
	if err != nil {
		return nil, err
	}

	// Set up client
	claudeClient := &ClaudeClient{
		MessageClient: client,
		UserLogin:     userLogin,
		Connector:     s.Connector,
		conversations: make(map[networkid.PortalID]*claudeapi.ConversationManager),
	}
	userLogin.Client = claudeClient

	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeComplete,
		StepID:       "complete",
		Instructions: "Successfully authenticated with claude.ai",
		CompleteParams: &bridgev2.LoginCompleteParams{
			UserLogin: userLogin,
		},
	}, nil
}

// extractSessionKey extracts the sessionKey value from a cookie string.
func extractSessionKey(cookies string) string {
	for _, part := range strings.Split(cookies, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "sessionKey=") {
			return strings.TrimPrefix(part, "sessionKey=")
		}
	}
	return ""
}

// Cancel cancels the login process.
func (s *SessionCookieLogin) Cancel() {}
