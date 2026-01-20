// Package connector provides the Matrix bridge connector for candy.ai.
package connector

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"
	"go.mau.fi/util/configupgrade"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"go.mau.fi/mautrix-candy/pkg/candygo"
)

// CandyConnector implements the bridgev2.NetworkConnector interface for candy.ai.
type CandyConnector struct {
	br     *bridgev2.Bridge
	Config Config
	Log    zerolog.Logger
}

var (
	_ bridgev2.NetworkConnector      = (*CandyConnector)(nil)
	_ bridgev2.MaxFileSizeingNetwork = (*CandyConnector)(nil)
)

// NewConnector creates a new candy.ai bridge connector.
func NewConnector() *CandyConnector {
	return &CandyConnector{}
}

// Init initializes the connector with the bridge.
func (c *CandyConnector) Init(bridge *bridgev2.Bridge) {
	c.br = bridge
	c.Log = bridge.Log.With().Str("connector", "candy").Logger()
}

// Start starts the connector.
func (c *CandyConnector) Start(ctx context.Context) error {
	c.Log.Info().Msg("Candy.ai connector starting")
	return nil
}

// GetName returns the name of the network.
func (c *CandyConnector) GetName() bridgev2.BridgeName {
	return bridgev2.BridgeName{
		DisplayName:      "Candy.ai",
		NetworkURL:       "https://candy.ai",
		NetworkIcon:      "mxc://maunium.net/candy",
		NetworkID:        "candy",
		BeeperBridgeType: "go.mau.fi/mautrix-candy",
		DefaultPort:      29350,
	}
}

// GetDBMetaTypes returns the database meta types for the connector.
func (c *CandyConnector) GetDBMetaTypes() database.MetaTypes {
	return database.MetaTypes{
		Ghost:     func() any { return &GhostMetadata{} },
		Message:   func() any { return &MessageMetadata{} },
		Portal:    func() any { return &PortalMetadata{} },
		Reaction:  nil,
		UserLogin: func() any { return &UserLoginMetadata{} },
	}
}

// GetCapabilities returns the capabilities of the connector.
func (c *CandyConnector) GetCapabilities() *bridgev2.NetworkGeneralCapabilities {
	return &bridgev2.NetworkGeneralCapabilities{
		DisappearingMessages: false,
		AggressiveUpdateInfo: false,
	}
}

// GetConfig returns the connector configuration.
func (c *CandyConnector) GetConfig() (example string, data any, upgrader configupgrade.Upgrader) {
	return ExampleConfig, &c.Config, nil
}

// SetMaxFileSize returns the maximum file size for uploads.
func (c *CandyConnector) SetMaxFileSize(maxSize int64) {
	// Candy.ai doesn't support file uploads in chat
}

// GetLoginFlows returns the available login flows.
func (c *CandyConnector) GetLoginFlows() []bridgev2.LoginFlow {
	return []bridgev2.LoginFlow{
		{
			Name:        "Email & Password",
			Description: "Log in with your candy.ai email and password",
			ID:          "password",
		},
		{
			Name:        "Session Cookie",
			Description: "Log in with an existing session cookie",
			ID:          "cookie",
		},
	}
}

// CreateLogin creates a new login handler.
func (c *CandyConnector) CreateLogin(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
	switch flowID {
	case "password":
		return &PasswordLogin{
			User:      user,
			Connector: c,
		}, nil
	case "cookie":
		return &CookieLogin{
			User:      user,
			Connector: c,
		}, nil
	default:
		return nil, fmt.Errorf("unknown login flow: %s", flowID)
	}
}

// LoadUserLogin loads an existing user login.
func (c *CandyConnector) LoadUserLogin(ctx context.Context, login *bridgev2.UserLogin) error {
	metadata := login.Metadata.(*UserLoginMetadata)

	client := candygo.NewClient(c.Log.With().Str("user", string(login.UserMXID)).Logger())

	if metadata.Cookie != "" {
		if err := client.LoginWithCookie(ctx, metadata.Cookie); err != nil {
			c.Log.Warn().Err(err).Msg("Failed to restore session from cookie")
			return err
		}
	} else {
		return fmt.Errorf("no stored session")
	}

	candyClient := &CandyClient{
		Client:    client,
		UserLogin: login,
		Connector: c,
	}

	login.Client = candyClient

	// Connect in background
	go candyClient.Connect(context.Background())

	return nil
}

// GhostMetadata contains candy.ai-specific ghost user metadata.
type GhostMetadata struct {
	ProfileID   int64  `json:"profile_id"`
	ProfileSlug string `json:"profile_slug"`
}

// MessageMetadata contains candy.ai-specific message metadata.
type MessageMetadata struct {
	CandyMessageID int64 `json:"candy_message_id"`
}

// PortalMetadata contains candy.ai-specific portal/room metadata.
type PortalMetadata struct {
	ConversationID int64  `json:"conversation_id"`
	ProfileID      int64  `json:"profile_id"`
	ProfileSlug    string `json:"profile_slug"`
	ProfileName    string `json:"profile_name"`
}

// UserLoginMetadata contains candy.ai-specific user login metadata.
type UserLoginMetadata struct {
	Cookie string `json:"cookie"`
	UserID int64  `json:"user_id"`
	Email  string `json:"email"`
}

// MakeCandyUserID creates a network user ID from a profile ID.
func MakeCandyUserID(profileID int64) networkid.UserID {
	return networkid.UserID(fmt.Sprintf("%d", profileID))
}

// MakeCandyPortalKey creates a portal key from a conversation ID.
func MakeCandyPortalKey(conversationID int64) networkid.PortalKey {
	return networkid.PortalKey{
		ID:       networkid.PortalID(fmt.Sprintf("%d", conversationID)),
		Receiver: "",
	}
}

// MakeCandyMessageID creates a message ID from a candy message ID.
func MakeCandyMessageID(messageID int64) networkid.MessageID {
	return networkid.MessageID(fmt.Sprintf("%d", messageID))
}

// ParseCandyPortalKey parses a portal key to get the conversation ID.
func ParseCandyPortalKey(key networkid.PortalKey) (int64, error) {
	var id int64
	_, err := fmt.Sscanf(string(key.ID), "%d", &id)
	return id, err
}

// ParseCandyUserID parses a user ID to get the profile ID.
func ParseCandyUserID(id networkid.UserID) (int64, error) {
	var profileID int64
	_, err := fmt.Sscanf(string(id), "%d", &profileID)
	return profileID, err
}
