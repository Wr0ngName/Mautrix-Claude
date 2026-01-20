package candygo

import (
	"context"
	"fmt"
	"io"
)

// GetConversations retrieves the list of conversations for the logged-in user.
func (c *Client) GetConversations(ctx context.Context) ([]*Conversation, error) {
	if !c.IsLoggedIn() {
		return nil, fmt.Errorf("not logged in")
	}

	resp, err := c.Get(ctx, "/conversations.turbo_stream")
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Parse the Turbo Stream response
	html := string(body)

	// Also try the regular conversations page if turbo stream fails
	if len(html) == 0 {
		html, err = c.GetHTML(ctx, "/conversations")
		if err != nil {
			return nil, fmt.Errorf("failed to load conversations: %w", err)
		}
	}

	// Update CSRF token
	c.csrf.UpdateFromHTML(html)

	return ParseConversationList(html)
}

// GetConversation retrieves a specific conversation by ID.
func (c *Client) GetConversation(ctx context.Context, conversationID int64) (*Conversation, error) {
	conversations, err := c.GetConversations(ctx)
	if err != nil {
		return nil, err
	}

	for _, conv := range conversations {
		if conv.ID == conversationID {
			return conv, nil
		}
	}

	return nil, fmt.Errorf("conversation %d not found", conversationID)
}

// GetConversationByProfileSlug finds a conversation by the AI profile slug.
func (c *Client) GetConversationByProfileSlug(ctx context.Context, slug string) (*Conversation, error) {
	conversations, err := c.GetConversations(ctx)
	if err != nil {
		return nil, err
	}

	for _, conv := range conversations {
		if conv.ProfileSlug == slug {
			return conv, nil
		}
	}

	return nil, fmt.Errorf("conversation with profile %s not found", slug)
}

// StartConversation starts or opens a conversation with an AI profile.
func (c *Client) StartConversation(ctx context.Context, profileSlug string) (*ConversationPageData, error) {
	// Load the profile page - this creates/opens the conversation
	data, err := c.LoadConversationPage(ctx, profileSlug)
	if err != nil {
		return nil, fmt.Errorf("failed to start conversation: %w", err)
	}

	return data, nil
}

// SyncConversation ensures we have the latest data for a conversation.
func (c *Client) SyncConversation(ctx context.Context, conversationID int64, profileSlug string) (*ConversationPageData, error) {
	// Load the conversation page to get fresh stream names and data
	data, err := c.LoadConversationPage(ctx, profileSlug)
	if err != nil {
		return nil, err
	}

	// Subscribe to WebSocket channels if connected
	if c.ws != nil && c.ws.IsConnected() {
		if err := c.ws.SubscribeToConversation(ctx, conversationID); err != nil {
			c.Log.Warn().Err(err).Int64("conversation_id", conversationID).Msg("Failed to subscribe to conversation")
		}
	}

	return data, nil
}

// GetAllProfiles fetches all available AI profiles from the home page.
func (c *Client) GetAllProfiles(ctx context.Context) ([]*Profile, error) {
	if !c.IsLoggedIn() {
		return nil, fmt.Errorf("not logged in")
	}

	html, err := c.GetHTML(ctx, "/")
	if err != nil {
		return nil, fmt.Errorf("failed to load homepage: %w", err)
	}

	return parseProfilesFromHomepage(html)
}

// parseProfilesFromHomepage extracts profile info from the homepage.
func parseProfilesFromHomepage(html string) ([]*Profile, error) {
	// This would parse the profile cards from the homepage
	// For now, return empty - profiles are better accessed via conversations
	return []*Profile{}, nil
}
