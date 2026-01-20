// Package candygo provides a Go client for the Candy.ai chat platform.
package candygo

import (
	"time"
)

// Session holds the authentication state for a candy.ai session.
type Session struct {
	Cookie    string `json:"cookie"`     // _chat_chat_session cookie value
	CSRFToken string `json:"csrf_token"` // Current CSRF token
	UserID    int64  `json:"user_id"`    // Numeric user ID
	UserGID   string `json:"user_gid"`   // Rails Global ID (gid://candy-ai/User/<id>)
	Email     string `json:"email"`      // User email
}

// Conversation represents a chat conversation with an AI character.
type Conversation struct {
	ID              int64     `json:"id"`
	ProfileID       int64     `json:"profile_id"`
	ProfileSlug     string    `json:"profile_slug"`
	ProfileName     string    `json:"profile_name"`
	ProfileImageURL string    `json:"profile_image_url"`
	LastMessage     string    `json:"last_message"`
	LastMessageAt   time.Time `json:"last_message_at"`
	UnreadCount     int       `json:"unread_count"`
}

// Profile represents an AI character profile.
type Profile struct {
	ID          int64  `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ImageURL    string `json:"image_url"`
	Age         int    `json:"age"`
	Personality string `json:"personality"`
}

// Message represents a chat message.
type Message struct {
	ID             int64     `json:"id"`
	ConversationID int64     `json:"conversation_id"`
	Body           string    `json:"body"`
	IsFromUser     bool      `json:"is_from_user"`
	Timestamp      time.Time `json:"timestamp"`
	ImageURLs      []string  `json:"image_urls,omitempty"`
}

// SendMessageRequest contains parameters for sending a message.
type SendMessageRequest struct {
	ProfileID      int64  `json:"profile_id"`
	Body           string `json:"body"`
	ImageGenToggle bool   `json:"image_gen_toggle"`
	NumImages      int    `json:"num_images"`
}

// TurboStreamAction represents a parsed Turbo Stream action.
type TurboStreamAction struct {
	Action   string // append, prepend, replace, remove, update
	Target   string // Target element ID
	Template string // HTML template content
}

// ChannelType represents the type of WebSocket channel.
type ChannelType string

const (
	ChannelMessageStream      ChannelType = "message_stream"
	ChannelConversationStream ChannelType = "conversation_stream"
	ChannelTokenBalance       ChannelType = "token_balance"
	ChannelNotification       ChannelType = "notification"
	ChannelVoiceButton        ChannelType = "voice_button"
	ChannelPhoneCallFeedback  ChannelType = "phone_call_feedback"
	ChannelPFPBanner          ChannelType = "pfp_banner"
)

// Event represents an event from candy.ai.
type Event interface {
	isEvent()
}

// MessageEvent is emitted when a new message is received.
type MessageEvent struct {
	Message      *Message
	Conversation *Conversation
}

func (*MessageEvent) isEvent() {}

// TypingEvent is emitted when the AI is typing.
type TypingEvent struct {
	ConversationID int64
	IsTyping       bool
}

func (*TypingEvent) isEvent() {}

// TokenBalanceEvent is emitted when the token balance changes.
type TokenBalanceEvent struct {
	Balance int64
}

func (*TokenBalanceEvent) isEvent() {}

// ConversationUpdateEvent is emitted when a conversation is updated.
type ConversationUpdateEvent struct {
	Conversation *Conversation
}

func (*ConversationUpdateEvent) isEvent() {}

// ConnectionStateEvent is emitted when the WebSocket connection state changes.
type ConnectionStateEvent struct {
	Connected bool
	Error     error
}

func (*ConnectionStateEvent) isEvent() {}

// EventHandler is a function that handles candy.ai events.
type EventHandler func(Event)

// ActionCableMessage represents a message in the ActionCable protocol.
type ActionCableMessage struct {
	Type       string `json:"type,omitempty"`
	Command    string `json:"command,omitempty"`
	Identifier string `json:"identifier,omitempty"`
	Message    any    `json:"message,omitempty"`
}

// ActionCableIdentifier is the channel identifier structure.
type ActionCableIdentifier struct {
	Channel          string `json:"channel"`
	SignedStreamName string `json:"signed_stream_name"`
}

// ChannelSubscription holds information about a subscribed channel.
type ChannelSubscription struct {
	SignedStreamName string
	ChannelType      ChannelType
	ResourceGID      string // e.g., gid://candy-ai/Conversation/123
	Subscribed       bool
}
