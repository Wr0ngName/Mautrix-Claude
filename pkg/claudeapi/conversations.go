// Package claudeapi provides a client for the Claude API.
package claudeapi

import (
	"sync"
	"time"
)

// ConversationManager manages conversation history and context.
type ConversationManager struct {
	messages   []Message
	maxTokens  int
	mu         sync.RWMutex
	createdAt  time.Time
	lastUsedAt time.Time
}

// NewConversationManager creates a new conversation manager.
func NewConversationManager(maxTokens int) *ConversationManager {
	now := time.Now()
	return &ConversationManager{
		messages:   make([]Message, 0),
		maxTokens:  maxTokens,
		createdAt:  now,
		lastUsedAt: now,
	}
}

// AddMessage adds a message to the conversation history.
func (cm *ConversationManager) AddMessage(role, content string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	message := Message{
		Role: role,
		Content: []Content{
			{
				Type: "text",
				Text: content,
			},
		},
	}

	cm.messages = append(cm.messages, message)
	cm.lastUsedAt = time.Now()
}

// GetMessages returns a copy of all messages in the conversation.
func (cm *ConversationManager) GetMessages() []Message {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	// Return a copy to prevent external modification
	messagesCopy := make([]Message, len(cm.messages))
	copy(messagesCopy, cm.messages)

	return messagesCopy
}

// Clear removes all messages from the conversation.
func (cm *ConversationManager) Clear() {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.messages = make([]Message, 0)
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
