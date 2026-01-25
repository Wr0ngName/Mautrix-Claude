package connector

import (
	"context"
	"fmt"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
)

// GetChatInfo returns information about a chat.
func (c *ClaudeClient) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	meta, ok := portal.Metadata.(*PortalMetadata)
	if !ok || meta == nil {
		return nil, fmt.Errorf("invalid portal metadata")
	}

	roomType := database.RoomTypeDM
	model := meta.Model
	if model == "" {
		model = c.Connector.Config.GetDefaultModel()
	}
	ghostID := MakeClaudeGhostID(model)

	name := meta.ConversationName
	if name == "" {
		name = fmt.Sprintf("Conversation with Claude (%s)", model)
	}

	return &bridgev2.ChatInfo{
		Name: &name,
		Members: &bridgev2.ChatMemberList{
			IsFull: true,
			Members: []bridgev2.ChatMember{
				{
					EventSender: bridgev2.EventSender{
						IsFromMe: false,
						Sender:   ghostID,
					},
				},
			},
		},
		Type: &roomType,
	}, nil
}
