// Package connector provides the Matrix bridge connector for Claude API.
package connector

import (
	"context"
	"fmt"
	"strings"

	"github.com/rs/zerolog"
	"go.mau.fi/util/configupgrade"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"go.mau.fi/mautrix-claude/pkg/claudeapi"
)

// ClaudeConnector implements the bridgev2.NetworkConnector interface for Claude API.
type ClaudeConnector struct {
	br     *bridgev2.Bridge
	Config Config
	Log    zerolog.Logger
}

var (
	_ bridgev2.NetworkConnector      = (*ClaudeConnector)(nil)
	_ bridgev2.MaxFileSizeingNetwork = (*ClaudeConnector)(nil)
)

// NewConnector creates a new Claude API bridge connector.
func NewConnector() *ClaudeConnector {
	return &ClaudeConnector{}
}

// Init initializes the connector with the bridge.
func (c *ClaudeConnector) Init(bridge *bridgev2.Bridge) {
	c.br = bridge
	c.Log = bridge.Log.With().Str("connector", "claude").Logger()
}

// Start starts the connector.
func (c *ClaudeConnector) Start(ctx context.Context) error {
	c.Log.Info().Msg("Claude API connector starting")
	return nil
}

// GetName returns the name of the network.
func (c *ClaudeConnector) GetName() bridgev2.BridgeName {
	return bridgev2.BridgeName{
		DisplayName:      "Claude AI",
		NetworkURL:       "https://claude.ai",
		NetworkIcon:      "mxc://maunium.net/claude",
		NetworkID:        "claude",
		BeeperBridgeType: "go.mau.fi/mautrix-claude",
		DefaultPort:      29320,
	}
}

// GetDBMetaTypes returns the database meta types for the connector.
func (c *ClaudeConnector) GetDBMetaTypes() database.MetaTypes {
	return database.MetaTypes{
		Ghost:     func() any { return &GhostMetadata{} },
		Message:   func() any { return &MessageMetadata{} },
		Portal:    func() any { return &PortalMetadata{} },
		Reaction:  nil,
		UserLogin: func() any { return &UserLoginMetadata{} },
	}
}

// GetCapabilities returns the capabilities of the connector.
func (c *ClaudeConnector) GetCapabilities() *bridgev2.NetworkGeneralCapabilities {
	return &bridgev2.NetworkGeneralCapabilities{
		DisappearingMessages: false,
		AggressiveUpdateInfo: false,
	}
}

// GetBridgeInfoVersion returns version numbers for bridge info and room capabilities.
// When the versions change, the bridge will automatically resend bridge info to all rooms.
func (c *ClaudeConnector) GetBridgeInfoVersion() (info, capabilities int) {
	return 1, 1
}

// GetConfig returns the connector configuration.
func (c *ClaudeConnector) GetConfig() (example string, data any, upgrader configupgrade.Upgrader) {
	return ExampleConfig, &c.Config, nil
}

// SetMaxFileSize returns the maximum file size for uploads.
func (c *ClaudeConnector) SetMaxFileSize(maxSize int64) {
	// Claude API supports images, but for now we don't implement file uploads
}

// GetLoginFlows returns the available login flows.
func (c *ClaudeConnector) GetLoginFlows() []bridgev2.LoginFlow {
	return []bridgev2.LoginFlow{
		{
			Name:        "API Key",
			Description: "Log in with your Claude API key from console.anthropic.com (pay-per-use)",
			ID:          "api_key",
		},
		{
			Name:        "Session Cookie",
			Description: "Log in with your claude.ai session cookie (Pro/Ultra subscription)",
			ID:          "session_cookie",
		},
	}
}

// CreateLogin creates a new login handler.
func (c *ClaudeConnector) CreateLogin(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
	switch flowID {
	case "api_key":
		return &APIKeyLogin{
			User:      user,
			Connector: c,
		}, nil
	case "session_cookie":
		return &SessionCookieLogin{
			User:      user,
			Connector: c,
		}, nil
	default:
		return nil, fmt.Errorf("unknown login flow: %s", flowID)
	}
}

// LoadUserLogin loads an existing user login.
func (c *ClaudeConnector) LoadUserLogin(ctx context.Context, login *bridgev2.UserLogin) error {
	metadata, ok := login.Metadata.(*UserLoginMetadata)
	if !ok || metadata == nil {
		return fmt.Errorf("invalid user login metadata")
	}

	log := c.Log.With().Str("user", string(login.UserMXID)).Logger()

	var client claudeapi.MessageClient

	switch metadata.AuthType {
	case "session_cookie", "web":
		if metadata.SessionKey == "" {
			return fmt.Errorf("no stored session cookie")
		}
		// SessionKey may store full cookie string (with cf_clearance etc.)
		sessionKey := extractSessionKeyFromCookies(metadata.SessionKey)
		if sessionKey == "" {
			// Backwards compatibility: SessionKey might be just the key value
			sessionKey = metadata.SessionKey
		}
		webClient := claudeapi.NewWebClient(sessionKey, log)
		// If the stored value contains semicolons, it's the full cookie string
		if strings.Contains(metadata.SessionKey, ";") {
			webClient.AllCookies = metadata.SessionKey
		}
		if metadata.OrganizationID != "" {
			webClient.OrganizationID = metadata.OrganizationID
		}
		client = webClient
	case "api_key", "":
		// Default to API key for backwards compatibility
		if metadata.APIKey == "" {
			return fmt.Errorf("no stored API key")
		}
		client = claudeapi.NewClient(metadata.APIKey, log)
	default:
		return fmt.Errorf("unknown auth type: %s", metadata.AuthType)
	}

	claudeClient := &ClaudeClient{
		MessageClient: client,
		UserLogin:     login,
		Connector:     c,
		conversations: make(map[networkid.PortalID]*claudeapi.ConversationManager),
		rateLimiter:   NewRateLimiter(c.Config.RateLimitPerMinute),
	}

	login.Client = claudeClient

	return nil
}

// GhostMetadata contains Claude-specific ghost user metadata.
type GhostMetadata struct {
	Model string `json:"model"` // Which Claude model this "ghost" represents
}

// MessageMetadata contains Claude-specific message metadata.
type MessageMetadata struct {
	ClaudeMessageID string `json:"claude_message_id"`
	TokensUsed      int    `json:"tokens_used"`
}

// PortalMetadata contains Claude-specific portal/room metadata.
type PortalMetadata struct {
	ConversationName string   `json:"conversation_name"`
	Model            string   `json:"model"`                   // Selected model for this room
	SystemPrompt     string   `json:"system_prompt,omitempty"` // Custom system prompt
	Temperature      *float64 `json:"temperature,omitempty"`   // Custom temperature (pointer to distinguish unset from 0)
}

// GetTemperature returns the temperature for this portal, or the default if not set.
// Returns defaultTemp if the value is nil or out of valid range (0-1).
func (p *PortalMetadata) GetTemperature(defaultTemp float64) float64 {
	if p.Temperature == nil {
		return defaultTemp
	}
	temp := *p.Temperature
	if temp < 0 || temp > 1 {
		return defaultTemp
	}
	return temp
}

// UserLoginMetadata contains Claude-specific user login metadata.
type UserLoginMetadata struct {
	// AuthType indicates the authentication method ("api_key" or "session_cookie")
	AuthType string `json:"auth_type,omitempty"`

	// API Key authentication (console.anthropic.com)
	APIKey string `json:"api_key,omitempty"`

	// Session cookie authentication (claude.ai)
	SessionKey     string `json:"session_key,omitempty"`
	OrganizationID string `json:"organization_id,omitempty"`

	// Display info
	Email string `json:"email,omitempty"`
}

// MakeClaudeGhostID creates a network user ID from a model name.
func MakeClaudeGhostID(model string) networkid.UserID {
	// Use model family for ghost ID to group similar models
	family := claudeapi.GetModelFamily(model)
	if family == "" {
		family = model
	}
	return networkid.UserID(fmt.Sprintf("claude_%s", family))
}

// MakeClaudePortalKey creates a portal key from a conversation identifier.
func MakeClaudePortalKey(conversationID string) networkid.PortalKey {
	return networkid.PortalKey{
		ID:       networkid.PortalID(conversationID),
		Receiver: "",
	}
}

// MakeClaudeMessageID creates a message ID from a Claude message ID.
func MakeClaudeMessageID(messageID string) networkid.MessageID {
	return networkid.MessageID(messageID)
}

// extractSessionKeyFromCookies extracts the sessionKey value from a cookie string.
func extractSessionKeyFromCookies(cookies string) string {
	for _, part := range strings.Split(cookies, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "sessionKey=") {
			return strings.TrimPrefix(part, "sessionKey=")
		}
	}
	return ""
}
