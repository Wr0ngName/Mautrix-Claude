package connector

import (
	"context"
	"fmt"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"go.mau.fi/mautrix-candy/pkg/candygo"
)

// ResolveIdentifier resolves a Matrix identifier to a candy.ai conversation.
func (c *CandyClient) ResolveIdentifier(ctx context.Context, identifier string, createChat bool) (*bridgev2.ResolveIdentifierResponse, error) {
	c.Connector.Log.Debug().Str("identifier", identifier).Msg("Resolving identifier")

	// The identifier could be a profile slug
	profileSlug := identifier

	// Try to find existing conversation
	conv, err := c.Client.GetConversationByProfileSlug(ctx, profileSlug)
	if err != nil {
		if !createChat {
			return nil, err
		}

		// Start a new conversation
		data, err := c.Client.StartConversation(ctx, profileSlug)
		if err != nil {
			return nil, fmt.Errorf("failed to start conversation: %w", err)
		}

		conv = &candygo.Conversation{
			ID:          data.ConversationID,
			ProfileID:   data.ProfileID,
			ProfileSlug: profileSlug,
		}
		if data.Profile != nil {
			conv.ProfileName = data.Profile.Name
		}
	}

	isBot := true
	resp := &bridgev2.ResolveIdentifierResponse{
		UserID: MakeCandyUserID(conv.ProfileID),
		UserInfo: &bridgev2.UserInfo{
			Name:  &conv.ProfileName,
			IsBot: &isBot,
		},
		Chat: &bridgev2.CreateChatResponse{
			PortalKey: MakeCandyPortalKey(conv.ID),
		},
	}

	return resp, nil
}

// GetChatInfoByChatID returns chat info for a specific chat ID.
func (c *CandyClient) GetChatInfoByChatID(ctx context.Context, chatID networkid.PortalID) (*bridgev2.ChatInfo, error) {
	convID, err := parsePortalID(chatID)
	if err != nil {
		return nil, err
	}

	conv, err := c.Client.GetConversation(ctx, convID)
	if err != nil {
		return nil, err
	}

	roomType := database.RoomTypeDM
	return &bridgev2.ChatInfo{
		Name: &conv.ProfileName,
		Type: &roomType,
		Members: &bridgev2.ChatMemberList{
			IsFull: true,
			Members: []bridgev2.ChatMember{
				{
					EventSender: bridgev2.EventSender{
						IsFromMe: false,
						Sender:   MakeCandyUserID(conv.ProfileID),
					},
				},
			},
		},
	}, nil
}

func parsePortalID(id networkid.PortalID) (int64, error) {
	var convID int64
	_, err := fmt.Sscanf(string(id), "%d", &convID)
	return convID, err
}

// FetchMessages requests historical messages for a conversation.
func (c *CandyClient) FetchMessages(ctx context.Context, fetchParams bridgev2.FetchMessagesParams) (*bridgev2.FetchMessagesResponse, error) {
	meta := fetchParams.Portal.Metadata.(*PortalMetadata)

	c.Connector.Log.Debug().
		Int64("conversation_id", meta.ConversationID).
		Msg("Fetching message history")

	var beforeID int64
	if fetchParams.AnchorMessage != nil {
		beforeID, _ = ParseCandyMessageID(fetchParams.AnchorMessage.ID)
	}

	messages, err := c.Client.LoadMessages(ctx, meta.ConversationID, beforeID)
	if err != nil {
		return nil, fmt.Errorf("failed to load messages: %w", err)
	}

	var backfillMessages []*bridgev2.BackfillMessage
	for _, msg := range messages {
		var sender bridgev2.EventSender
		if msg.IsFromUser {
			sender = bridgev2.EventSender{IsFromMe: true}
		} else {
			sender = bridgev2.EventSender{
				IsFromMe: false,
				Sender:   MakeCandyUserID(meta.ProfileID),
			}
		}

		backfillMessages = append(backfillMessages, &bridgev2.BackfillMessage{
			ConvertedMessage: &bridgev2.ConvertedMessage{
				Parts: []*bridgev2.ConvertedMessagePart{
					{
						Type: event.EventMessage,
						Content: &event.MessageEventContent{
							MsgType: event.MsgText,
							Body:    msg.Body,
						},
					},
				},
			},
			Sender:    sender,
			ID:        MakeCandyMessageID(msg.ID),
			Timestamp: msg.Timestamp,
		})
	}

	return &bridgev2.FetchMessagesResponse{
		Messages: backfillMessages,
		HasMore:  len(messages) > 0,
		Forward:  false,
	}, nil
}

// ParseCandyMessageID parses a network message ID to get the candy message ID.
func ParseCandyMessageID(id networkid.MessageID) (int64, error) {
	var msgID int64
	_, err := fmt.Sscanf(string(id), "%d", &msgID)
	return msgID, err
}
