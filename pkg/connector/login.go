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

var (
	_ bridgev2.LoginProcess          = (*APIKeyLogin)(nil)
	_ bridgev2.LoginProcessUserInput = (*APIKeyLogin)(nil)
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
			APIKey: apiKey,
		},
	}, nil)
	if err != nil {
		return nil, err
	}

	// Set up client with rate limiter
	claudeClient := &ClaudeClient{
		MessageClient: client,
		UserLogin:     userLogin,
		Connector:     a.Connector,
		conversations: make(map[networkid.PortalID]*claudeapi.ConversationManager),
		rateLimiter:   NewRateLimiter(a.Connector.Config.GetRateLimitPerMinute()),
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

// SidecarLogin handles login when sidecar mode is enabled.
// No API key is needed - authentication uses mounted ~/.claude credentials.
type SidecarLogin struct {
	User      *bridgev2.User
	Connector *ClaudeConnector
}

var _ bridgev2.LoginProcess = (*SidecarLogin)(nil)

// Start begins the sidecar login flow.
func (s *SidecarLogin) Start(ctx context.Context) (*bridgev2.LoginStep, error) {
	// Verify sidecar is healthy and authenticated before allowing login
	client := s.Connector.getSidecarClient()
	if err := client.Validate(ctx); err != nil {
		// Provide helpful error message for admin
		return nil, fmt.Errorf("sidecar not ready: %w\n\n"+
			"The bridge admin needs to set up Claude Code credentials:\n"+
			"1. On a machine with a browser, run: claude\n"+
			"2. Complete the OAuth authentication\n"+
			"3. Copy credentials to the bridge: cp -r ~/.claude/* /data/.claude/\n"+
			"4. Restart the bridge container", err)
	}

	// Generate a unique login ID for this user
	loginID := networkid.UserLoginID(fmt.Sprintf("sidecar_%s", strings.ReplaceAll(string(s.User.MXID), ":", "_")))

	userLogin, err := s.User.NewLogin(ctx, &database.UserLogin{
		ID:         loginID,
		RemoteName: "Claude (Pro/Max)",
		Metadata: &UserLoginMetadata{
			APIKey: "", // No API key needed for sidecar
		},
	}, nil)
	if err != nil {
		return nil, err
	}

	// Set up client with sidecar backend
	claudeClient := &ClaudeClient{
		MessageClient: client,
		UserLogin:     userLogin,
		Connector:     s.Connector,
		conversations: make(map[networkid.PortalID]*claudeapi.ConversationManager),
		rateLimiter:   NewRateLimiter(s.Connector.Config.GetRateLimitPerMinute()),
	}
	userLogin.Client = claudeClient

	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeComplete,
		StepID:       "complete",
		Instructions: "Successfully connected to Claude via Pro/Max subscription",
		CompleteParams: &bridgev2.LoginCompleteParams{
			UserLogin: userLogin,
		},
	}, nil
}

// Cancel cancels the login process.
func (s *SidecarLogin) Cancel() {}
