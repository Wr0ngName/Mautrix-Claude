package connector

import (
	"context"
	"fmt"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
)

// GetChatInfo returns information about a chat.
func (c *ClaudeClient) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	meta := portal.Metadata.(*PortalMetadata)

	roomType := database.RoomTypeDM
	ghostID := MakeClaudeGhostID(meta.Model)

	name := meta.ConversationName
	if name == "" {
		name = fmt.Sprintf("Conversation with Claude (%s)", meta.Model)
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
