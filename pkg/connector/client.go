package connector

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"

	"go.mau.fi/mautrix-claude/pkg/claudeapi"
)

// ClaudeClient represents a client connection to Claude API.
type ClaudeClient struct {
	Client        *claudeapi.Client
	UserLogin     *bridgev2.UserLogin
	Connector     *ClaudeConnector
	conversations map[networkid.PortalID]*claudeapi.ConversationManager
	convMu        sync.RWMutex

	// Graceful shutdown support
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

var _ bridgev2.NetworkAPI = (*ClaudeClient)(nil)

// Connect is called when the client should connect.
func (c *ClaudeClient) Connect(ctx context.Context) {
	// Create a cancellable context derived from parent for proper propagation
	c.ctx, c.cancel = context.WithCancel(ctx)

	// Start conversation cleanup goroutine if max age is configured
	if c.Connector.Config.ConversationMaxAge > 0 {
		c.wg.Add(1)
		go c.conversationCleanupLoop()
	}

	c.Connector.Log.Info().Msg("Claude client ready")
	c.UserLogin.BridgeState.Send(status.BridgeState{
		StateEvent: status.StateConnected,
	})
}

// Disconnect is called when the client should disconnect.
func (c *ClaudeClient) Disconnect() {
	// Cancel context to stop all goroutines
	if c.cancel != nil {
		c.cancel()
	}

	// Wait for all goroutines to finish
	c.wg.Wait()

	c.Connector.Log.Info().Msg("Claude client disconnected")
}

// conversationCleanupLoop periodically cleans up expired conversations.
func (c *ClaudeClient) conversationCleanupLoop() {
	defer c.wg.Done()

	maxAge := time.Duration(c.Connector.Config.ConversationMaxAge) * time.Hour
	ticker := time.NewTicker(10 * time.Minute) // Check every 10 minutes
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.cleanupExpiredConversations(maxAge)
		}
	}
}

// cleanupExpiredConversations removes conversations that have exceeded the max age.
func (c *ClaudeClient) cleanupExpiredConversations(maxAge time.Duration) {
	c.convMu.Lock()
	defer c.convMu.Unlock()

	expired := make([]networkid.PortalID, 0)

	for portalID, cm := range c.conversations {
		if cm.IsExpired(maxAge) {
			expired = append(expired, portalID)
		}
	}

	for _, portalID := range expired {
		delete(c.conversations, portalID)
		c.Connector.Log.Debug().
			Str("portal_id", string(portalID)).
			Msg("Cleaned up expired conversation")
	}

	if len(expired) > 0 {
		c.Connector.Log.Info().
			Int("count", len(expired)).
			Msg("Cleaned up expired conversations")
	}
}

// IsLoggedIn checks if the client is logged in.
func (c *ClaudeClient) IsLoggedIn() bool {
	return c.Client != nil && c.Client.APIKey != ""
}

// LogoutRemote logs out from the remote service.
func (c *ClaudeClient) LogoutRemote(ctx context.Context) {
	// API keys don't need remote logout
}

// IsThisUser checks if a user ID belongs to this logged-in user.
func (c *ClaudeClient) IsThisUser(ctx context.Context, userID networkid.UserID) bool {
	// All Claude ghosts belong to the system, not individual users
	return false
}

// getConversationManager gets or creates a conversation manager for a portal.
func (c *ClaudeClient) getConversationManager(portal *bridgev2.Portal) *claudeapi.ConversationManager {
	c.convMu.Lock()
	defer c.convMu.Unlock()

	portalID := portal.PortalKey.ID

	if cm, ok := c.conversations[portalID]; ok {
		return cm
	}

	// Create new conversation manager with max tokens from config
	maxTokens := claudeapi.GetModelMaxTokens(c.Connector.Config.GetDefaultModel())
	cm := claudeapi.NewConversationManager(maxTokens)
	c.conversations[portalID] = cm

	return cm
}

// HandleMatrixMessage handles a message sent from Matrix to Claude.
func (c *ClaudeClient) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	meta, ok := msg.Portal.Metadata.(*PortalMetadata)
	if !ok || meta == nil {
		return nil, fmt.Errorf("invalid portal metadata")
	}

	// Get or create conversation manager for this portal
	convMgr := c.getConversationManager(msg.Portal)

	// Add user message to history with Matrix event ID for tracking
	userMsgID := string(msg.Event.ID)
	convMgr.AddMessageWithID("user", msg.Content.Body, userMsgID)

	// Prepare API request
	model := meta.Model
	if model == "" {
		model = c.Connector.Config.GetDefaultModel()
	}

	temperature := meta.GetTemperature(c.Connector.Config.GetTemperature())

	systemPrompt := meta.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = c.Connector.Config.GetSystemPrompt()
	}

	req := &claudeapi.CreateMessageRequest{
		Model:       model,
		Messages:    convMgr.GetMessages(),
		MaxTokens:   c.Connector.Config.GetMaxTokens(),
		Temperature: temperature,
		System:      systemPrompt,
		Stream:      true, // Use streaming for better UX
	}

	// Send to Claude API
	stream, err := c.Client.CreateMessageStream(ctx, req)
	if err != nil {
		c.Connector.Log.Error().Err(err).Msg("Failed to create message stream")
		return nil, c.formatUserFriendlyError(err)
	}
	if stream == nil {
		return nil, fmt.Errorf("received nil stream from Claude API")
	}

	// Collect response
	var responseText strings.Builder
	var claudeMessageID string
	var inputTokens, outputTokens int
	var streamError error

	for event := range stream {
		switch event.Type {
		case "message_start":
			if event.Message != nil {
				claudeMessageID = event.Message.ID
				if event.Message.Usage != nil && event.Message.Usage.InputTokens > 0 {
					inputTokens = event.Message.Usage.InputTokens
				}
			}
		case "content_block_delta":
			if event.Delta != nil && event.Delta.Text != "" {
				responseText.WriteString(event.Delta.Text)
			}
		case "message_delta":
			if event.Usage != nil {
				outputTokens = event.Usage.OutputTokens
			}
		case "error":
			c.Connector.Log.Error().Interface("event", event).Msg("Error in stream")
			if event.Error != nil {
				streamError = fmt.Errorf("streaming error: %s - %s", event.Error.Type, event.Error.Message)
			} else {
				streamError = fmt.Errorf("unknown streaming error")
			}
		}
	}

	// Check for streaming errors
	if streamError != nil {
		return nil, streamError
	}

	if claudeMessageID == "" {
		claudeMessageID = fmt.Sprintf("msg_%d", time.Now().UnixNano())
	}

	// Add assistant response to conversation history
	responseContent := responseText.String()
	if responseContent == "" {
		return nil, fmt.Errorf("received empty response from Claude")
	}

	convMgr.AddMessageWithID("assistant", responseContent, claudeMessageID)

	// Trim conversation if needed
	if err := convMgr.TrimToTokenLimit(); err != nil {
		c.Connector.Log.Warn().Err(err).Msg("Failed to trim conversation")
	}

	// Queue the assistant's response as an incoming message
	// Use a context-aware goroutine with WaitGroup for graceful shutdown
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()

		// Check if already shutting down before queuing
		if c.ctx.Err() != nil {
			c.Connector.Log.Debug().Msg("Skipping assistant response queue due to shutdown")
			return
		}
		c.queueAssistantResponse(msg.Portal, responseContent, claudeMessageID, inputTokens+outputTokens)
	}()

	// Return response metadata
	return &bridgev2.MatrixMessageResponse{
		DB: &database.Message{
			ID:        MakeClaudeMessageID(claudeMessageID),
			Timestamp: time.Now(),
			Metadata: &MessageMetadata{
				ClaudeMessageID: claudeMessageID,
				TokensUsed:      inputTokens + outputTokens,
			},
		},
	}, nil
}

// formatUserFriendlyError converts API errors to user-friendly messages.
func (c *ClaudeClient) formatUserFriendlyError(err error) error {
	if err == nil {
		return nil
	}

	// Check for specific error types
	if claudeapi.IsRateLimitError(err) {
		retryAfter := claudeapi.GetRetryAfter(err)
		if retryAfter > 0 {
			return fmt.Errorf("rate limit exceeded. Please wait %s and try again", retryAfter.Round(time.Second))
		}
		return fmt.Errorf("rate limit exceeded. Please wait a moment and try again")
	}

	if claudeapi.IsAuthError(err) {
		return fmt.Errorf("authentication failed. Please check your API key is valid and has sufficient permissions")
	}

	if claudeapi.IsOverloadedError(err) {
		return fmt.Errorf("Claude is currently overloaded. Please try again in a few moments")
	}

	if claudeapi.IsInvalidRequestError(err) {
		return fmt.Errorf("invalid request: %v", err)
	}

	// Generic error
	return fmt.Errorf("failed to send message to Claude: %w", err)
}

// queueAssistantResponse sends the assistant's message to the Matrix room.
func (c *ClaudeClient) queueAssistantResponse(portal *bridgev2.Portal, text, messageID string, tokensUsed int) {
	model := c.Connector.Config.GetDefaultModel()
	if meta, ok := portal.Metadata.(*PortalMetadata); ok && meta != nil && meta.Model != "" {
		model = meta.Model
	}

	ghostID := MakeClaudeGhostID(model)

	c.UserLogin.QueueRemoteEvent(&simplevent.Message[*MessageMetadata]{
		EventMeta: simplevent.EventMeta{
			Type: bridgev2.RemoteEventMessage,
			LogContext: func(c zerolog.Context) zerolog.Context {
				return c.Str("claude_message_id", messageID)
			},
			PortalKey: portal.PortalKey,
			Sender:    bridgev2.EventSender{Sender: ghostID},
			Timestamp: time.Now(),
		},
		ID: MakeClaudeMessageID(messageID),
		Data: &MessageMetadata{
			ClaudeMessageID: messageID,
			TokensUsed:      tokensUsed,
		},
		ConvertMessageFunc: func(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, data *MessageMetadata) (*bridgev2.ConvertedMessage, error) {
			// Convert markdown to Matrix HTML format
			content := format.RenderMarkdown(text, true, true)
			content.MsgType = event.MsgText
			return &bridgev2.ConvertedMessage{
				Parts: []*bridgev2.ConvertedMessagePart{
					{
						ID:      networkid.PartID(messageID),
						Type:    event.EventMessage,
						Content: &content,
					},
				},
			}, nil
		},
	})
}

// GetCapabilities returns the capabilities for a specific portal.
func (c *ClaudeClient) GetCapabilities(ctx context.Context, portal *bridgev2.Portal) *event.RoomFeatures {
	return &event.RoomFeatures{
		Formatting: event.FormattingFeatureMap{
			event.FmtBold:          event.CapLevelFullySupported,
			event.FmtItalic:        event.CapLevelFullySupported,
			event.FmtStrikethrough: event.CapLevelFullySupported,
			event.FmtInlineCode:    event.CapLevelFullySupported,
			event.FmtCodeBlock:     event.CapLevelFullySupported,
		},
		MaxTextLength:       100000, // Claude has large context window
		Edit:                event.CapLevelFullySupported,
		Delete:              event.CapLevelFullySupported,
		Reaction:            event.CapLevelUnsupported,
		Reply:               event.CapLevelPartialSupport, // Could implement as conversation context
		ReadReceipts:        false,
		TypingNotifications: false,
	}
}

// HandleMatrixEdit handles an edit to a Matrix message.
// When a user edits a message, we update the conversation history and remove
// any subsequent messages (since the conversation flow has changed).
func (c *ClaudeClient) HandleMatrixEdit(ctx context.Context, msg *bridgev2.MatrixEdit) error {
	// Get the conversation manager for this portal
	convMgr := c.getConversationManager(msg.Portal)

	// Get the original message ID being edited
	originalMsgID := string(msg.EditTarget.ID)

	// Get the new content
	newContent := msg.Content.Body

	// Try to edit by the original message ID
	if convMgr.EditMessageByID(originalMsgID, newContent) {
		c.Connector.Log.Debug().
			Str("message_id", originalMsgID).
			Str("new_content", newContent[:min(50, len(newContent))]).
			Msg("Edited message in conversation history")
		return nil
	}

	// If message not found by ID, try to edit the last user message
	// This handles cases where the message ID wasn't tracked
	if err := convMgr.EditLastUserMessage(newContent); err != nil {
		c.Connector.Log.Warn().
			Str("message_id", originalMsgID).
			Err(err).
			Msg("Could not find message to edit")
		return fmt.Errorf("message not found in conversation history")
	}

	c.Connector.Log.Debug().
		Str("message_id", originalMsgID).
		Msg("Edited last user message in conversation history")
	return nil
}

// HandleMatrixMessageRemove handles a deletion of a Matrix message.
// When a user deletes a message, we remove it from the conversation history
// along with any subsequent messages (since the conversation flow is broken).
func (c *ClaudeClient) HandleMatrixMessageRemove(ctx context.Context, msg *bridgev2.MatrixMessageRemove) error {
	// Get the conversation manager for this portal
	convMgr := c.getConversationManager(msg.Portal)

	// Get the message ID being deleted
	deletedMsgID := string(msg.TargetMessage.ID)

	// Try to delete by message ID
	if convMgr.DeleteMessageByID(deletedMsgID) {
		c.Connector.Log.Debug().
			Str("message_id", deletedMsgID).
			Msg("Deleted message from conversation history")
		return nil
	}

	// If message not found by ID, try to delete the last user message
	// This handles cases where the message ID wasn't tracked
	if err := convMgr.DeleteLastUserMessage(); err != nil {
		c.Connector.Log.Warn().
			Str("message_id", deletedMsgID).
			Err(err).
			Msg("Could not find message to delete")
		return fmt.Errorf("message not found in conversation history")
	}

	c.Connector.Log.Debug().
		Str("message_id", deletedMsgID).
		Msg("Deleted last user message from conversation history")
	return nil
}

// HandleMatrixReaction handles a reaction to a Matrix message (not supported).
func (c *ClaudeClient) HandleMatrixReaction(ctx context.Context, msg *bridgev2.MatrixReaction) error {
	return fmt.Errorf("reactions are not supported")
}

// HandleMatrixReactionRemove handles removal of a reaction (not supported).
func (c *ClaudeClient) HandleMatrixReactionRemove(ctx context.Context, msg *bridgev2.MatrixReactionRemove) error {
	return fmt.Errorf("reactions are not supported")
}

// HandleMatrixReadReceipt handles a read receipt (not supported).
func (c *ClaudeClient) HandleMatrixReadReceipt(ctx context.Context, msg *bridgev2.MatrixReadReceipt) error {
	// Silently ignore read receipts
	return nil
}

// HandleMatrixTyping handles a typing notification (not supported).
func (c *ClaudeClient) HandleMatrixTyping(ctx context.Context, msg *bridgev2.MatrixTyping) error {
	// Silently ignore typing notifications
	return nil
}

// PreHandleMatrixMessage is called before handling a Matrix message.
func (c *ClaudeClient) PreHandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (bridgev2.MatrixMessageResponse, error) {
	// No pre-processing needed
	return bridgev2.MatrixMessageResponse{}, nil
}

// GetMetrics returns the API client metrics.
func (c *ClaudeClient) GetMetrics() *claudeapi.Metrics {
	if c.Client == nil {
		return nil
	}
	return c.Client.GetMetrics()
}

// ClearConversation clears the conversation history for a portal.
func (c *ClaudeClient) ClearConversation(portalID networkid.PortalID) {
	c.convMu.Lock()
	defer c.convMu.Unlock()

	if cm, ok := c.conversations[portalID]; ok {
		cm.Clear()
		c.Connector.Log.Debug().
			Str("portal_id", string(portalID)).
			Msg("Cleared conversation history")
	}
}

// GetConversationStats returns stats for a portal's conversation.
func (c *ClaudeClient) GetConversationStats(portalID networkid.PortalID) (messageCount, estimatedTokens int, lastUsed time.Time) {
	c.convMu.RLock()
	defer c.convMu.RUnlock()

	if cm, ok := c.conversations[portalID]; ok {
		return cm.MessageCount(), cm.EstimatedTokens(), cm.LastUsedAt()
	}
	return 0, 0, time.Time{}
}
