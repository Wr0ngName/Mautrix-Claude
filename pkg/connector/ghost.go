package connector

import (
	"context"
	"fmt"

	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

// GetGhostInfo implements getting information about a ghost user (AI character).
func (c *CandyClient) GetGhostInfo(ctx context.Context, userID networkid.UserID) (*bridgev2.UserInfo, error) {
	profileID, err := ParseCandyUserID(userID)
	if err != nil {
		return nil, fmt.Errorf("invalid user ID: %w", err)
	}

	// Try to find this profile in our known conversations
	conversations, err := c.Client.GetConversations(ctx)
	if err != nil {
		return nil, err
	}

	for _, conv := range conversations {
		if conv.ProfileID == profileID {
			return &bridgev2.UserInfo{
				Name:        &conv.ProfileName,
				IsBot:       ptr.Ptr(true),
				Identifiers: []string{fmt.Sprintf("candy:%d", profileID)},
			}, nil
		}
	}

	// Not found in conversations, return minimal info
	return &bridgev2.UserInfo{
		IsBot: ptr.Ptr(true),
	}, nil
}

// CreateGhostMetadata creates metadata for a new ghost user.
func CreateGhostMetadata(profileID int64, profileSlug string) *GhostMetadata {
	return &GhostMetadata{
		ProfileID:   profileID,
		ProfileSlug: profileSlug,
	}
}
