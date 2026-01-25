// Package claudeapi provides a client for the Claude API.
package claudeapi

import (
	"fmt"
	"sync"
	"time"
)

// TrackedMessage wraps a Message with an external ID for tracking.
type TrackedMessage struct {
	Message
	ExternalID string // External ID (e.g., Matrix message ID)
}

// ConversationManager manages conversation history and context.
type ConversationManager struct {
	messages   []TrackedMessage
	maxTokens  int
	mu         sync.RWMutex
	createdAt  time.Time
	lastUsedAt time.Time
}

// NewConversationManager creates a new conversation manager.
func NewConversationManager(maxTokens int) *ConversationManager {
	now := time.Now()
	return &ConversationManager{
		messages:   make([]TrackedMessage, 0),
		maxTokens:  maxTokens,
		createdAt:  now,
		lastUsedAt: now,
	}
}

// AddMessage adds a message to the conversation history.
func (cm *ConversationManager) AddMessage(role, content string) {
	cm.AddMessageWithID(role, content, "")
}

// AddMessageWithID adds a message with an external ID for tracking.
func (cm *ConversationManager) AddMessageWithID(role, content, externalID string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	message := TrackedMessage{
		Message: Message{
			Role: role,
			Content: []Content{
				{
					Type: "text",
					Text: content,
				},
			},
		},
		ExternalID: externalID,
	}

	cm.messages = append(cm.messages, message)
	cm.lastUsedAt = time.Now()
}

// AddMessageWithContent adds a message with arbitrary content (text, images, etc).
// Use this when you have content that includes images or mixed content types.
func (cm *ConversationManager) AddMessageWithContent(role string, content []Content, externalID string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	message := TrackedMessage{
		Message: Message{
			Role:    role,
			Content: content,
		},
		ExternalID: externalID,
	}

	cm.messages = append(cm.messages, message)
	cm.lastUsedAt = time.Now()
}

// GetMessages returns a copy of all messages in the conversation (without tracking info).
func (cm *ConversationManager) GetMessages() []Message {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	// Return plain Messages for API calls
	messagesCopy := make([]Message, len(cm.messages))
	for i, tm := range cm.messages {
		messagesCopy[i] = tm.Message
	}

	return messagesCopy
}

// EditMessageByID edits a message by its external ID.
// If the message is found, it updates the content and removes all subsequent messages
// (since changing a message invalidates all following responses).
// Returns true if the message was found and edited.
func (cm *ConversationManager) EditMessageByID(externalID, newContent string) bool {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	for i, tm := range cm.messages {
		if tm.ExternalID == externalID {
			// Update the message content
			cm.messages[i].Content = []Content{
				{
					Type: "text",
					Text: newContent,
				},
			}

			// Remove all messages after this one (they're now invalid)
			cm.messages = cm.messages[:i+1]

			cm.lastUsedAt = time.Now()
			return true
		}
	}

	return false
}

// DeleteMessageByID deletes a message by its external ID.
// Also removes all subsequent messages (since the conversation flow is broken).
// Returns true if the message was found and deleted.
func (cm *ConversationManager) DeleteMessageByID(externalID string) bool {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	for i, tm := range cm.messages {
		if tm.ExternalID == externalID {
			// Remove this message and all after it
			cm.messages = cm.messages[:i]

			cm.lastUsedAt = time.Now()
			return true
		}
	}

	return false
}

// GetMessageByID returns the content of a message by its external ID.
func (cm *ConversationManager) GetMessageByID(externalID string) (string, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	for _, tm := range cm.messages {
		if tm.ExternalID == externalID {
			if len(tm.Content) > 0 {
				return tm.Content[0].Text, true
			}
			return "", true
		}
	}

	return "", false
}

// EditLastUserMessage edits the most recent user message.
// Removes any assistant messages that came after it.
// Returns an error if there's no user message to edit.
func (cm *ConversationManager) EditLastUserMessage(newContent string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Find the last user message
	for i := len(cm.messages) - 1; i >= 0; i-- {
		if cm.messages[i].Role == "user" {
			// Update content
			cm.messages[i].Content = []Content{
				{
					Type: "text",
					Text: newContent,
				},
			}

			// Remove all messages after this one
			cm.messages = cm.messages[:i+1]

			cm.lastUsedAt = time.Now()
			return nil
		}
	}

	return fmt.Errorf("no user message found to edit")
}

// DeleteLastUserMessage deletes the most recent user message and its response.
// Returns an error if there's no user message to delete.
func (cm *ConversationManager) DeleteLastUserMessage() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Find the last user message
	for i := len(cm.messages) - 1; i >= 0; i-- {
		if cm.messages[i].Role == "user" {
			// Remove this message and all after it
			cm.messages = cm.messages[:i]

			cm.lastUsedAt = time.Now()
			return nil
		}
	}

	return fmt.Errorf("no user message found to delete")
}

// Clear removes all messages from the conversation.
func (cm *ConversationManager) Clear() {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.messages = make([]TrackedMessage, 0)
	cm.lastUsedAt = time.Now()
}

// TrimToTokenLimit trims old messages to stay within the token limit.
// This uses a simplified token estimation where ~4 characters equals ~1 token.
// See ApproxCharsPerToken constant for details.
func (cm *ConversationManager) TrimToTokenLimit() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cm.maxTokens <= 0 {
		// No limit
		return nil
	}

	// Estimate total tokens using the approximate chars-per-token ratio
	totalChars := 0
	for _, msg := range cm.messages {
		for _, content := range msg.Content {
			totalChars += len(content.Text)
		}
	}

	estimatedTokens := totalChars / ApproxCharsPerToken

	// If we're under the limit, no trimming needed
	if estimatedTokens < cm.maxTokens {
		return nil
	}

	// If we're at or over the limit, trim to target percentage of max to provide headroom
	targetChars := (cm.maxTokens * ApproxCharsPerToken * ContextTrimTargetPercent) / 100

	// Remove oldest messages until we're under the target
	// Keep at least the minimum number of messages (typically one user-assistant pair)
	for len(cm.messages) > MinMessagesToKeep && totalChars > targetChars {
		// Remove the oldest message
		removed := cm.messages[0]
		cm.messages = cm.messages[1:]

		// Update character count
		for _, content := range removed.Content {
			totalChars -= len(content.Text)
		}
	}

	return nil
}

// MessageCount returns the number of messages in the conversation.
func (cm *ConversationManager) MessageCount() int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return len(cm.messages)
}

// EstimatedTokens returns the estimated token count for the conversation.
func (cm *ConversationManager) EstimatedTokens() int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	totalChars := 0
	for _, msg := range cm.messages {
		for _, content := range msg.Content {
			totalChars += len(content.Text)
		}
	}

	return totalChars / ApproxCharsPerToken
}

// GetMaxTokens returns the maximum token limit for this conversation.
func (cm *ConversationManager) GetMaxTokens() int {
	return cm.maxTokens
}

// SetMaxTokens sets a new maximum token limit.
func (cm *ConversationManager) SetMaxTokens(maxTokens int) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.maxTokens = maxTokens
}

// CreatedAt returns when the conversation was created.
func (cm *ConversationManager) CreatedAt() time.Time {
	return cm.createdAt
}

// LastUsedAt returns when the conversation was last used.
func (cm *ConversationManager) LastUsedAt() time.Time {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.lastUsedAt
}

// Age returns the age of the conversation since creation.
func (cm *ConversationManager) Age() time.Duration {
	return time.Since(cm.createdAt)
}

// IdleTime returns how long since the conversation was last used.
func (cm *ConversationManager) IdleTime() time.Duration {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return time.Since(cm.lastUsedAt)
}

// IsExpired checks if the conversation has exceeded the given max age.
// A max age of 0 means the conversation never expires.
func (cm *ConversationManager) IsExpired(maxAge time.Duration) bool {
	if maxAge <= 0 {
		return false
	}
	return cm.IdleTime() > maxAge
}

// HasMessages returns true if the conversation has any messages.
func (cm *ConversationManager) HasMessages() bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return len(cm.messages) > 0
}

// LastMessageRole returns the role of the last message, or empty string if no messages.
func (cm *ConversationManager) LastMessageRole() string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if len(cm.messages) == 0 {
		return ""
	}
	return cm.messages[len(cm.messages)-1].Role
}
