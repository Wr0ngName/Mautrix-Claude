package candygo

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"regexp"
	"strconv"
	"sync"
)

// SendMessage sends a message to an AI character.
func (c *Client) SendMessage(ctx context.Context, req *SendMessageRequest) (*Message, error) {
	if !c.IsLoggedIn() {
		return nil, fmt.Errorf("not logged in")
	}

	c.Log.Debug().
		Int64("profile_id", req.ProfileID).
		Str("body", truncate(req.Body, 50)).
		Msg("Sending message")

	// Build multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Add authenticity token
	writer.WriteField("authenticity_token", c.csrf.GetToken())

	// Add image gen toggle fields (always include both as seen in captures)
	writer.WriteField("image_gen_toggle", "0")
	if req.ImageGenToggle {
		writer.WriteField("image_gen_toggle", "1")
	} else {
		writer.WriteField("image_gen_toggle", "1") // Default behavior from captures
	}

	// Add message fields
	writer.WriteField("message[profile_id]", strconv.FormatInt(req.ProfileID, 10))
	writer.WriteField("message[body]", req.Body)

	// Add gen_ai_suggestion fields
	numImages := req.NumImages
	if numImages == 0 {
		numImages = 1
	}
	writer.WriteField("gen_ai_suggestion[number_of_images]", strconv.Itoa(numImages))
	writer.WriteField("gen_ai_suggestion[gen_ai_suggestion_id]", "")
	writer.WriteField("gen_ai_suggestion[gen_ai_prompt_id]", "")

	writer.Close()

	// Create request
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/messages", &buf)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", writer.FormDataContentType())
	httpReq.Header.Set("X-Requested-With", "XMLHttpRequest")
	httpReq.Header.Set("X-Turbo-Request-Id", generateTurboRequestID())

	// Send request
	resp, err := c.do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnprocessableEntity {
		// Likely CSRF token expired, try to refresh and retry
		c.Log.Debug().Msg("Got 422, refreshing CSRF token")
		if err := c.csrf.Refresh(ctx); err != nil {
			return nil, fmt.Errorf("failed to refresh CSRF: %w", err)
		}
		// Retry once
		return c.SendMessage(ctx, req)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response - it's a Turbo Stream
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// The response contains the user message confirmation
	// The AI response will come via WebSocket
	actions, err := ParseTurboStream(string(body))
	if err != nil {
		c.Log.Warn().Err(err).Msg("Failed to parse response Turbo Stream")
		return nil, nil
	}

	// Try to extract the sent message
	for _, action := range actions {
		if msg, _ := ExtractMessageFromTurboStream(&action); msg != nil {
			return msg, nil
		}
	}

	c.Log.Debug().Msg("Message sent successfully (no message in response)")
	return nil, nil
}

// LoadMessages loads message history for a conversation.
func (c *Client) LoadMessages(ctx context.Context, conversationID int64, beforeMessageID int64) ([]*Message, error) {
	if !c.IsLoggedIn() {
		return nil, fmt.Errorf("not logged in")
	}

	path := fmt.Sprintf("/messages/load.turbo_stream?conversation_id=%d", conversationID)
	if beforeMessageID > 0 {
		path += fmt.Sprintf("&before_message_id=%d", beforeMessageID)
	}

	resp, err := c.Get(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return ParseMessageHistory(string(body))
}

// LoadConversationPage loads a conversation page and extracts necessary data.
func (c *Client) LoadConversationPage(ctx context.Context, profileSlug string) (*ConversationPageData, error) {
	if !c.IsLoggedIn() {
		return nil, fmt.Errorf("not logged in")
	}

	path := "/ai-girlfriend/" + profileSlug

	html, err := c.GetHTML(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("failed to load page: %w", err)
	}

	// Update CSRF token
	c.csrf.UpdateFromHTML(html)

	data := &ConversationPageData{}

	// Extract profile info
	profile, err := ExtractProfileInfo(html)
	if err == nil && profile != nil {
		data.Profile = profile
		data.ProfileID = profile.ID
	}

	// Extract signed stream names
	data.StreamNames = ExtractSignedStreamNames(html)

	// Extract conversation ID if present
	data.ConversationID = extractConversationIDFromHTML(html)

	c.Log.Debug().
		Int64("profile_id", data.ProfileID).
		Int64("conversation_id", data.ConversationID).
		Int("stream_names", len(data.StreamNames)).
		Msg("Loaded conversation page")

	// Cache stream names in WebSocket client if connected
	if c.ws != nil && data.ConversationID != 0 {
		c.ws.SetConversationStreamNames(data.ConversationID, data.StreamNames)
	}

	return data, nil
}

// ConversationPageData contains data extracted from a conversation page.
type ConversationPageData struct {
	ConversationID int64
	ProfileID      int64
	Profile        *Profile
	StreamNames    map[string]string
}

// extractConversationIDFromHTML extracts conversation ID from page HTML.
func extractConversationIDFromHTML(html string) int64 {
	// Try various patterns
	patterns := []string{
		`data-conversation-id="(\d+)"`,
		`conversation[_-]id['":\s]+(\d+)`,
		`gid://candy-ai/Conversation/(\d+)`,
	}

	for _, pattern := range patterns {
		re, err := compileRegexp(pattern)
		if err != nil {
			continue
		}
		if matches := re.FindStringSubmatch(html); len(matches) > 1 {
			id, _ := strconv.ParseInt(matches[1], 10, 64)
			if id > 0 {
				return id
			}
		}
	}

	return 0
}

var regexpCache = make(map[string]*regexp.Regexp)
var regexpCacheMu = sync.Mutex{}

func compileRegexp(pattern string) (*regexp.Regexp, error) {
	regexpCacheMu.Lock()
	defer regexpCacheMu.Unlock()

	if cached, ok := regexpCache[pattern]; ok {
		return cached, nil
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}

	regexpCache[pattern] = re
	return re, nil
}
