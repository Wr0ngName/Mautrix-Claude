package candygo

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

const (
	actionCableProtocol = "actioncable-v1-json"
	turboStreamChannel  = "Turbo::StreamsChannel"
)

var randSource = rand.New(rand.NewSource(time.Now().UnixNano()))

// ActionCableClient handles WebSocket communication with candy.ai.
type ActionCableClient struct {
	client       *Client
	conn         *websocket.Conn
	connected    bool
	mu           sync.RWMutex
	channels     map[string]*ChannelSubscription
	channelMu    sync.RWMutex
	stopChan     chan struct{}
	pingTimer    *time.Ticker

	// Stream names cache (per conversation)
	streamNames     map[int64]map[string]string
	streamNamesMu   sync.RWMutex
}

// NewActionCableClient creates a new ActionCable WebSocket client.
func NewActionCableClient(client *Client) *ActionCableClient {
	return &ActionCableClient{
		client:      client,
		channels:    make(map[string]*ChannelSubscription),
		streamNames: make(map[int64]map[string]string),
		stopChan:    make(chan struct{}),
	}
}

// Connect establishes the WebSocket connection.
func (ac *ActionCableClient) Connect(ctx context.Context) error {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	if ac.connected {
		return nil
	}

	wsURL := fmt.Sprintf("wss://%s/cable", "candy.ai")

	config, err := websocket.NewConfig(wsURL, ac.client.BaseURL)
	if err != nil {
		return fmt.Errorf("failed to create websocket config: %w", err)
	}

	// Set headers
	config.Header = http.Header{
		"User-Agent":             {ac.client.UserAgent},
		"Origin":                 {ac.client.BaseURL},
		"Sec-WebSocket-Protocol": {actionCableProtocol + ", actioncable-unsupported"},
	}

	// Add cookies from jar
	session := ac.client.GetSession()
	if session != nil && session.Cookie != "" {
		config.Header.Set("Cookie", "_chat_chat_session="+session.Cookie)
	}

	ac.client.Log.Debug().Str("url", wsURL).Msg("Connecting to WebSocket")

	conn, err := websocket.DialConfig(config)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	ac.conn = conn
	ac.connected = true

	// Start message handler
	go ac.readLoop()

	// Wait for welcome message
	time.Sleep(100 * time.Millisecond)

	ac.client.Log.Info().Msg("WebSocket connected")
	ac.client.emitEvent(&ConnectionStateEvent{Connected: true})

	return nil
}

// Close closes the WebSocket connection.
func (ac *ActionCableClient) Close() {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	if !ac.connected {
		return
	}

	close(ac.stopChan)

	if ac.pingTimer != nil {
		ac.pingTimer.Stop()
	}

	if ac.conn != nil {
		ac.conn.Close()
	}

	ac.connected = false
	ac.client.emitEvent(&ConnectionStateEvent{Connected: false})
}

// IsConnected returns whether the WebSocket is connected.
func (ac *ActionCableClient) IsConnected() bool {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	return ac.connected
}

// readLoop reads messages from the WebSocket.
func (ac *ActionCableClient) readLoop() {
	for {
		select {
		case <-ac.stopChan:
			return
		default:
			var raw string
			err := websocket.Message.Receive(ac.conn, &raw)
			if err != nil {
				ac.client.Log.Error().Err(err).Msg("WebSocket read error")
				ac.handleDisconnect(err)
				return
			}

			ac.handleMessage([]byte(raw))
		}
	}
}

// handleMessage processes an incoming WebSocket message.
func (ac *ActionCableClient) handleMessage(data []byte) {
	var msg ActionCableMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		ac.client.Log.Error().Err(err).Str("data", string(data)).Msg("Failed to parse ActionCable message")
		return
	}

	switch msg.Type {
	case "welcome":
		ac.client.Log.Debug().Msg("Received ActionCable welcome")

	case "confirm_subscription":
		ac.client.Log.Debug().Str("identifier", msg.Identifier).Msg("Subscription confirmed")
		ac.markSubscribed(msg.Identifier)

	case "reject_subscription":
		ac.client.Log.Warn().Str("identifier", msg.Identifier).Msg("Subscription rejected")

	case "ping":
		// Ping messages - no action needed

	case "disconnect":
		ac.client.Log.Warn().Msg("Server requested disconnect")
		ac.handleDisconnect(nil)

	case "":
		// Data message
		if msg.Message != nil {
			ac.handleDataMessage(msg.Identifier, msg.Message)
		}
	}
}

// handleDataMessage processes a data message from a channel.
func (ac *ActionCableClient) handleDataMessage(identifier string, message any) {
	// Message is typically HTML for Turbo Streams
	htmlStr, ok := message.(string)
	if !ok {
		ac.client.Log.Debug().Interface("message", message).Msg("Non-string message received")
		return
	}

	// Parse Turbo Stream actions
	actions, err := ParseTurboStream(htmlStr)
	if err != nil {
		ac.client.Log.Error().Err(err).Msg("Failed to parse Turbo Stream")
		return
	}

	for _, action := range actions {
		ac.processTurboStreamAction(&action)
	}
}

// processTurboStreamAction processes a single Turbo Stream action.
func (ac *ActionCableClient) processTurboStreamAction(action *TurboStreamAction) {
	ac.client.Log.Debug().
		Str("action", action.Action).
		Str("target", action.Target).
		Msg("Processing Turbo Stream action")

	// Check if this is a message update
	if action.Target == "messages-list" || action.Action == "append" || action.Action == "prepend" {
		msg, err := ExtractMessageFromTurboStream(action)
		if err != nil {
			ac.client.Log.Error().Err(err).Msg("Failed to extract message")
			return
		}

		if msg != nil && msg.ID != 0 {
			ac.client.Log.Debug().
				Int64("id", msg.ID).
				Bool("from_user", msg.IsFromUser).
				Str("body", truncate(msg.Body, 50)).
				Msg("Received message")

			ac.client.emitEvent(&MessageEvent{
				Message: msg,
			})
		}
	}

	// Check if this is a conversation update
	if conv, _ := ExtractConversationUpdate(action); conv != nil && conv.ID != 0 {
		ac.client.emitEvent(&ConversationUpdateEvent{
			Conversation: conv,
		})
	}
}

// handleDisconnect handles a WebSocket disconnection.
func (ac *ActionCableClient) handleDisconnect(err error) {
	ac.mu.Lock()
	wasConnected := ac.connected
	ac.connected = false
	ac.mu.Unlock()

	if wasConnected {
		ac.client.emitEvent(&ConnectionStateEvent{
			Connected: false,
			Error:     err,
		})
	}
}

// Subscribe subscribes to a channel with the given signed stream name.
func (ac *ActionCableClient) Subscribe(signedStreamName string, channelType ChannelType, resourceGID string) error {
	if !ac.IsConnected() {
		return fmt.Errorf("not connected")
	}

	identifier := ActionCableIdentifier{
		Channel:          turboStreamChannel,
		SignedStreamName: signedStreamName,
	}

	identifierJSON, err := json.Marshal(identifier)
	if err != nil {
		return fmt.Errorf("failed to marshal identifier: %w", err)
	}

	msg := ActionCableMessage{
		Command:    "subscribe",
		Identifier: string(identifierJSON),
	}

	if err := ac.sendMessage(msg); err != nil {
		return err
	}

	// Store subscription info
	ac.channelMu.Lock()
	ac.channels[signedStreamName] = &ChannelSubscription{
		SignedStreamName: signedStreamName,
		ChannelType:      channelType,
		ResourceGID:      resourceGID,
		Subscribed:       false,
	}
	ac.channelMu.Unlock()

	return nil
}

// Unsubscribe unsubscribes from a channel.
func (ac *ActionCableClient) Unsubscribe(signedStreamName string) error {
	if !ac.IsConnected() {
		return nil
	}

	identifier := ActionCableIdentifier{
		Channel:          turboStreamChannel,
		SignedStreamName: signedStreamName,
	}

	identifierJSON, err := json.Marshal(identifier)
	if err != nil {
		return fmt.Errorf("failed to marshal identifier: %w", err)
	}

	msg := ActionCableMessage{
		Command:    "unsubscribe",
		Identifier: string(identifierJSON),
	}

	if err := ac.sendMessage(msg); err != nil {
		return err
	}

	ac.channelMu.Lock()
	delete(ac.channels, signedStreamName)
	ac.channelMu.Unlock()

	return nil
}

// SubscribeToConversation subscribes to all channels for a conversation.
func (ac *ActionCableClient) SubscribeToConversation(ctx context.Context, conversationID int64) error {
	// Check if we have cached stream names
	ac.streamNamesMu.RLock()
	names, exists := ac.streamNames[conversationID]
	ac.streamNamesMu.RUnlock()

	if !exists {
		// Need to load the conversation page to get stream names
		if err := ac.loadConversationStreamNames(ctx, conversationID); err != nil {
			return err
		}

		ac.streamNamesMu.RLock()
		names = ac.streamNames[conversationID]
		ac.streamNamesMu.RUnlock()
	}

	if names == nil {
		return fmt.Errorf("no stream names available for conversation %d", conversationID)
	}

	resourceGID := fmt.Sprintf("gid://candy-ai/Conversation/%d", conversationID)

	// Subscribe to message_stream
	if msgStream, ok := names["message_stream"]; ok {
		if err := ac.Subscribe(msgStream, ChannelMessageStream, resourceGID); err != nil {
			return err
		}
	}

	// Subscribe to conversation_stream
	if convStream, ok := names["conversation_stream"]; ok {
		if err := ac.Subscribe(convStream, ChannelConversationStream, resourceGID); err != nil {
			return err
		}
	}

	return nil
}

// loadConversationStreamNames loads stream names from a conversation page.
func (ac *ActionCableClient) loadConversationStreamNames(ctx context.Context, conversationID int64) error {
	// We need to find the profile slug for this conversation
	// For now, we'll need this to be provided externally or found via other means
	// This is a simplified approach - in practice, we'd maintain a mapping

	ac.client.Log.Debug().Int64("conversation_id", conversationID).Msg("Loading conversation stream names")

	// The stream names should be loaded when we access the conversation
	// This will be handled by the higher-level conversation loading

	return nil
}

// SetConversationStreamNames caches stream names for a conversation.
func (ac *ActionCableClient) SetConversationStreamNames(conversationID int64, names map[string]string) {
	ac.streamNamesMu.Lock()
	ac.streamNames[conversationID] = names
	ac.streamNamesMu.Unlock()
}

// markSubscribed marks a channel as successfully subscribed.
func (ac *ActionCableClient) markSubscribed(identifier string) {
	var id ActionCableIdentifier
	if err := json.Unmarshal([]byte(identifier), &id); err != nil {
		return
	}

	ac.channelMu.Lock()
	if sub, ok := ac.channels[id.SignedStreamName]; ok {
		sub.Subscribed = true
	}
	ac.channelMu.Unlock()
}

// sendMessage sends a message over the WebSocket.
func (ac *ActionCableClient) sendMessage(msg ActionCableMessage) error {
	ac.mu.RLock()
	conn := ac.conn
	ac.mu.RUnlock()

	if conn == nil {
		return fmt.Errorf("not connected")
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	ac.client.Log.Debug().Str("command", msg.Command).Msg("Sending ActionCable message")

	return websocket.Message.Send(conn, string(data))
}

// truncate truncates a string to the specified length.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
