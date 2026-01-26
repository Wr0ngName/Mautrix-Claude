package connector

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
// Users provide their Claude Code credentials JSON for Pro/Max subscription access.
type SidecarLogin struct {
	User      *bridgev2.User
	Connector *ClaudeConnector
}

var (
	_ bridgev2.LoginProcess          = (*SidecarLogin)(nil)
	_ bridgev2.LoginProcessUserInput = (*SidecarLogin)(nil)
)

// Start begins the sidecar login flow.
func (s *SidecarLogin) Start(ctx context.Context) (*bridgev2.LoginStep, error) {
	return &bridgev2.LoginStep{
		Type:   bridgev2.LoginStepTypeUserInput,
		StepID: "credentials",
		Instructions: "To use your Claude Pro/Max subscription:\n\n" +
			"1. On your computer, run: claude\n" +
			"2. Complete the browser authentication\n" +
			"3. Run: cat ~/.claude/.credentials.json\n" +
			"4. Copy the JSON content and paste it below\n\n" +
			"(Your credentials are stored securely and only used for this bridge)",
		UserInputParams: &bridgev2.LoginUserInputParams{
			Fields: []bridgev2.LoginInputDataField{
				{
					Type:        bridgev2.LoginInputFieldTypePassword,
					ID:          "credentials_json",
					Name:        "Credentials JSON",
					Description: "Contents of ~/.claude/.credentials.json",
				},
			},
		},
	}, nil
}

// SubmitUserInput processes the submitted credentials.
func (s *SidecarLogin) SubmitUserInput(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	credentialsJSON := input["credentials_json"]

	if credentialsJSON == "" {
		return nil, fmt.Errorf("credentials JSON is required")
	}

	// Validate it's valid JSON
	var creds map[string]any
	if err := json.Unmarshal([]byte(credentialsJSON), &creds); err != nil {
		return nil, fmt.Errorf("invalid JSON format: %w", err)
	}

	// Check for required fields (access_token or similar)
	if _, ok := creds["claudeAiOauth"]; !ok {
		if _, ok := creds["access_token"]; !ok {
			return nil, fmt.Errorf("invalid credentials: missing authentication data")
		}
	}

	// TODO: Store credentials per-user and have sidecar use them
	// For now, sidecar uses global credentials from /data/.claude/
	// This is a limitation - all users share the same subscription

	// Verify sidecar is healthy
	client := s.Connector.getSidecarClient()
	if err := client.Validate(ctx); err != nil {
		return nil, fmt.Errorf("sidecar not available - please contact bridge admin: %w", err)
	}

	// Generate a unique login ID for this user
	loginID := networkid.UserLoginID(fmt.Sprintf("sidecar_%s", strings.ReplaceAll(string(s.User.MXID), ":", "_")))

	userLogin, err := s.User.NewLogin(ctx, &database.UserLogin{
		ID:         loginID,
		RemoteName: "Claude (Pro/Max)",
		Metadata: &UserLoginMetadata{
			APIKey:          "", // No API key for sidecar
			CredentialsJSON: credentialsJSON,
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
