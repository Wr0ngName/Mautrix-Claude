package connector

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridge/status"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"

	"go.mau.fi/mautrix-candy/pkg/candygo"
)

// CandyClient implements the bridgev2.NetworkAPI interface for a candy.ai user.
type CandyClient struct {
	*candygo.Client
	UserLogin *bridgev2.UserLogin
	Connector *CandyConnector

	// Track subscribed conversations
	subscribedConversations map[int64]bool
}

var _ bridgev2.NetworkAPI = (*CandyClient)(nil)

// Connect establishes the connection to candy.ai.
func (c *CandyClient) Connect(ctx context.Context) {
	c.Connector.Log.Debug().Msg("Connecting to candy.ai")

	if err := c.Client.Connect(ctx); err != nil {
		c.Connector.Log.Error().Err(err).Msg("Failed to connect to candy.ai")
		c.UserLogin.BridgeState.Send(status.BridgeState{
			StateEvent: status.StateBadCredentials,
			Error:      "candy-connect-failed",
			Message:    err.Error(),
		})
		return
	}

	// Set up event handler
	c.Client.SetEventHandler(c.handleCandyEvent)

	c.UserLogin.BridgeState.Send(status.BridgeState{
		StateEvent: status.StateConnected,
	})

	// Sync conversations if enabled
	if c.Connector.Config.SyncOnConnect {
		go c.syncConversations(context.Background())
	}
}

// Disconnect disconnects from candy.ai.
func (c *CandyClient) Disconnect() {
	c.Connector.Log.Debug().Msg("Disconnecting from candy.ai")
	c.Client.Disconnect()
}

// IsLoggedIn returns whether the client is logged in.
func (c *CandyClient) IsLoggedIn() bool {
	return c.Client.IsLoggedIn()
}

// LogoutRemote logs out from the remote service.
func (c *CandyClient) LogoutRemote(ctx context.Context) {
	c.Client.Logout(ctx)
}

// IsThisUser checks if the given user ID belongs to this user.
func (c *CandyClient) IsThisUser(ctx context.Context, userID networkid.UserID) bool {
	session := c.Client.GetSession()
	if session == nil {
		return false
	}
	return string(userID) == fmt.Sprintf("%d", session.UserID)
}

// GetChatInfo returns information about a chat.
func (c *CandyClient) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	meta := portal.Metadata.(*PortalMetadata)

	// Load conversation data
	data, err := c.Client.LoadConversationPage(ctx, meta.ProfileSlug)
	if err != nil {
		return nil, err
	}

	roomType := database.RoomTypeDM
	members := &bridgev2.ChatMemberList{
		IsFull: true,
		Members: []bridgev2.ChatMember{
			{
				EventSender: bridgev2.EventSender{
					IsFromMe: false,
					Sender:   MakeCandyUserID(meta.ProfileID),
				},
			},
		},
	}

	return &bridgev2.ChatInfo{
		Name:    &meta.ProfileName,
		Members: members,
		Type:    &roomType,
		ExtraUpdates: func(ctx context.Context, p *bridgev2.Portal) bool {
			pm := p.Metadata.(*PortalMetadata)
			pm.ConversationID = data.ConversationID
			pm.ProfileID = data.ProfileID
			if data.Profile != nil {
				pm.ProfileName = data.Profile.Name
			}
			return true
		},
	}, nil
}

// GetUserInfo returns information about a user.
func (c *CandyClient) GetUserInfo(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	meta := ghost.Metadata.(*GhostMetadata)

	// Load profile info
	data, err := c.Client.LoadConversationPage(ctx, meta.ProfileSlug)
	if err != nil {
		return nil, err
	}

	isBot := true
	info := &bridgev2.UserInfo{
		IsBot: &isBot,
	}

	if data.Profile != nil {
		info.Name = &data.Profile.Name
	}

	return info, nil
}

// GetCapabilities returns the capabilities for a portal.
func (c *CandyClient) GetCapabilities(ctx context.Context, portal *bridgev2.Portal) *bridgev2.NetworkRoomCapabilities {
	return &bridgev2.NetworkRoomCapabilities{
		FormattedText:    true,
		UserMentions:     false,
		RoomMentions:     false,
		LocationMessages: false,
		Captions:         false,
		MaxTextLength:    10000,
		Edits:            false,
		EditMaxCount:     0,
		EditMaxAge:       0,
		Deletes:          false,
		DeleteMaxAge:     0,
		Reactions:        false,
		ReactionCount:    0,
		Replies:          false,
		Threads:          false,
		ReadReceipts:     false,
	}
}

// HandleMatrixMessage handles a message from Matrix.
func (c *CandyClient) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (message *bridgev2.MatrixMessageResponse, err error) {
	meta := msg.Portal.Metadata.(*PortalMetadata)

	// Send message to candy.ai
	req := &candygo.SendMessageRequest{
		ProfileID: meta.ProfileID,
		Body:      msg.Content.Body,
	}

	candyMsg, err := c.Client.SendMessage(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to send message: %w", err)
	}

	resp := &bridgev2.MatrixMessageResponse{
		DB: &database.Message{
			Timestamp: time.Now(),
		},
	}

	if candyMsg != nil {
		resp.DB.ID = MakeCandyMessageID(candyMsg.ID)
		resp.DB.Metadata = &MessageMetadata{CandyMessageID: candyMsg.ID}
	}

	return resp, nil
}

// syncConversations syncs all conversations from candy.ai.
func (c *CandyClient) syncConversations(ctx context.Context) {
	c.Connector.Log.Debug().Msg("Syncing conversations")

	conversations, err := c.Client.GetConversations(ctx)
	if err != nil {
		c.Connector.Log.Error().Err(err).Msg("Failed to get conversations")
		return
	}

	for _, conv := range conversations {
		c.Connector.Log.Debug().
			Int64("id", conv.ID).
			Str("profile", conv.ProfileSlug).
			Msg("Found conversation")

		// Ensure portal exists
		portal, err := c.UserLogin.Bridge.GetPortalByKey(ctx, MakeCandyPortalKey(conv.ID))
		if err != nil {
			c.Connector.Log.Error().Err(err).Msg("Failed to get portal")
			continue
		}

		if portal == nil {
			c.Connector.Log.Debug().Int64("conversation_id", conv.ID).Msg("Creating portal")
		}

		// Subscribe to conversation updates
		if c.subscribedConversations == nil {
			c.subscribedConversations = make(map[int64]bool)
		}

		if !c.subscribedConversations[conv.ID] {
			if err := c.Client.SubscribeToConversation(ctx, conv.ID); err != nil {
				c.Connector.Log.Warn().Err(err).Int64("conversation_id", conv.ID).Msg("Failed to subscribe")
			} else {
				c.subscribedConversations[conv.ID] = true
			}
		}
	}
}

// handleCandyEvent handles events from the candy.ai client.
func (c *CandyClient) handleCandyEvent(evt candygo.Event) {
	switch e := evt.(type) {
	case *candygo.MessageEvent:
		c.handleMessageEvent(e)
	case *candygo.ConversationUpdateEvent:
		c.handleConversationUpdateEvent(e)
	case *candygo.ConnectionStateEvent:
		c.handleConnectionStateEvent(e)
	}
}

// handleMessageEvent handles incoming messages from candy.ai.
func (c *CandyClient) handleMessageEvent(evt *candygo.MessageEvent) {
	if evt.Message == nil {
		return
	}

	msg := evt.Message
	c.Connector.Log.Debug().
		Int64("id", msg.ID).
		Bool("from_user", msg.IsFromUser).
		Str("body", truncateStr(msg.Body, 50)).
		Msg("Received message from candy.ai")

	// Skip messages from the user (we sent them)
	if msg.IsFromUser {
		return
	}

	// Find the portal for this conversation
	if evt.Conversation == nil {
		c.Connector.Log.Warn().Msg("Message event without conversation info")
		return
	}

	portalKey := MakeCandyPortalKey(evt.Conversation.ID)

	// Get the portal metadata to find sender info
	portal, err := c.UserLogin.Bridge.GetPortalByKey(context.Background(), portalKey)
	if err != nil || portal == nil {
		c.Connector.Log.Warn().Err(err).Msg("Portal not found for message")
		return
	}

	meta := portal.Metadata.(*PortalMetadata)

	// Create the Matrix event
	wrapped := &simplevent.Message[*MessageMetadata]{
		EventMeta: simplevent.EventMeta{
			Type: bridgev2.RemoteEventMessage,
			LogContext: func(c zerolog.Context) zerolog.Context {
				return c.Int64("candy_msg_id", msg.ID)
			},
			PortalKey:    portalKey,
			CreatePortal: true,
			Sender: bridgev2.EventSender{
				IsFromMe: false,
				Sender:   MakeCandyUserID(meta.ProfileID),
			},
			Timestamp: msg.Timestamp,
		},
		ID: MakeCandyMessageID(msg.ID),
		Data: &MessageMetadata{
			CandyMessageID: msg.ID,
		},
		ConvertMessageFunc: func(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, data *MessageMetadata) (*bridgev2.ConvertedMessage, error) {
			return &bridgev2.ConvertedMessage{
				Parts: []*bridgev2.ConvertedMessagePart{
					{
						Type: event.EventMessage,
						Content: &event.MessageEventContent{
							MsgType: event.MsgText,
							Body:    msg.Body,
						},
					},
				},
			}, nil
		},
	}

	c.UserLogin.Bridge.QueueRemoteEvent(c.UserLogin, wrapped)
}

// handleConversationUpdateEvent handles conversation update events.
func (c *CandyClient) handleConversationUpdateEvent(evt *candygo.ConversationUpdateEvent) {
	c.Connector.Log.Debug().
		Int64("conversation_id", evt.Conversation.ID).
		Str("last_message", truncateStr(evt.Conversation.LastMessage, 50)).
		Msg("Conversation updated")
}

// handleConnectionStateEvent handles connection state changes.
func (c *CandyClient) handleConnectionStateEvent(evt *candygo.ConnectionStateEvent) {
	if evt.Connected {
		c.Connector.Log.Info().Msg("WebSocket connected")
		c.UserLogin.BridgeState.Send(status.BridgeState{
			StateEvent: status.StateConnected,
		})
	} else {
		c.Connector.Log.Warn().Err(evt.Error).Msg("WebSocket disconnected")
		c.UserLogin.BridgeState.Send(status.BridgeState{
			StateEvent: status.StateTransientDisconnect,
			Error:      "candy-websocket-disconnected",
		})
	}
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
