package candygo

import (
	"encoding/base64"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

var (
	// Regex patterns for extracting data from HTML
	messageIDRegex       = regexp.MustCompile(`message_id_(\d+)`)
	conversationIDRegex  = regexp.MustCompile(`conversation-(\d+)`)
	profileIDRegex       = regexp.MustCompile(`profile_id["']?\s*[:=]\s*["']?(\d+)`)
	signedStreamRegex    = regexp.MustCompile(`signed_stream_name["']?\s*[:=]\s*["']([^"']+)["']`)
	timestampRegex       = regexp.MustCompile(`(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z?)`)
	lastMessageTextRegex = regexp.MustCompile(`last-message-text-value=["']([^"']+)["']`)
)

// ParseTurboStream parses a Turbo Stream HTML response into actions.
func ParseTurboStream(html string) ([]TurboStreamAction, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}

	var actions []TurboStreamAction

	doc.Find("turbo-stream").Each(func(i int, s *goquery.Selection) {
		action, _ := s.Attr("action")
		target, _ := s.Attr("target")

		// Get template content
		template := s.Find("template").First()
		templateHTML, _ := template.Html()

		actions = append(actions, TurboStreamAction{
			Action:   action,
			Target:   target,
			Template: templateHTML,
		})
	})

	return actions, nil
}

// ExtractMessageFromTurboStream extracts a message from a Turbo Stream action.
func ExtractMessageFromTurboStream(action *TurboStreamAction) (*Message, error) {
	if action.Template == "" {
		return nil, nil
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(action.Template))
	if err != nil {
		return nil, err
	}

	msg := &Message{}

	// Find message container
	messageDiv := doc.Find("[id^='message_id_']").First()
	if messageDiv.Length() == 0 {
		// Try alternate format
		messageDiv = doc.Find(".user-response, .user-message").First()
	}

	if messageDiv.Length() == 0 {
		return nil, nil
	}

	// Extract message ID
	if id, exists := messageDiv.Attr("id"); exists {
		if matches := messageIDRegex.FindStringSubmatch(id); len(matches) > 1 {
			msg.ID, _ = strconv.ParseInt(matches[1], 10, 64)
		}
	}

	// Determine if from user or AI
	class, _ := messageDiv.Attr("class")
	msg.IsFromUser = strings.Contains(class, "user-message")

	// Extract message body text
	// Try various selectors for message content
	bodySelectors := []string{
		".message-body",
		"[data-messages--assistant-edit-form-target='view']",
		".text-white",
		"p",
	}

	for _, selector := range bodySelectors {
		bodyElem := messageDiv.Find(selector).First()
		if bodyElem.Length() > 0 {
			msg.Body = strings.TrimSpace(bodyElem.Text())
			if msg.Body != "" {
				break
			}
		}
	}

	// If still no body, try getting all text content
	if msg.Body == "" {
		msg.Body = strings.TrimSpace(messageDiv.Text())
	}

	// Clean up the body
	msg.Body = cleanMessageBody(msg.Body)

	// Extract images if any
	messageDiv.Find("img").Each(func(i int, s *goquery.Selection) {
		if src, exists := s.Attr("src"); exists {
			// Filter out UI icons
			if !strings.Contains(src, "/assets/") && !strings.Contains(src, ".svg") {
				msg.ImageURLs = append(msg.ImageURLs, src)
			}
		}
	})

	// Set timestamp to now if not found
	msg.Timestamp = time.Now()

	return msg, nil
}

// ExtractConversationUpdate extracts conversation update from Turbo Stream.
func ExtractConversationUpdate(action *TurboStreamAction) (*Conversation, error) {
	if action.Template == "" {
		return nil, nil
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(action.Template))
	if err != nil {
		return nil, err
	}

	conv := &Conversation{}

	// Try to extract from data attributes
	doc.Find("[data-conversations--delayed-sidebar-update-conversation-id-value]").Each(func(i int, s *goquery.Selection) {
		if idStr, exists := s.Attr("data-conversations--delayed-sidebar-update-conversation-id-value"); exists {
			conv.ID, _ = strconv.ParseInt(idStr, 10, 64)
		}
		if lastMsg, exists := s.Attr("data-conversations--delayed-sidebar-update-last-message-text-value"); exists {
			conv.LastMessage = lastMsg
		}
		if ts, exists := s.Attr("data-conversations--delayed-sidebar-update-last-message-timestamp-value"); exists {
			conv.LastMessageAt, _ = time.Parse(time.RFC3339, ts)
		}
	})

	// Extract from target if possible
	if conv.ID == 0 && action.Target != "" {
		if matches := conversationIDRegex.FindStringSubmatch(action.Target); len(matches) > 1 {
			conv.ID, _ = strconv.ParseInt(matches[1], 10, 64)
		}
	}

	if conv.ID == 0 {
		return nil, nil
	}

	return conv, nil
}

// ExtractSignedStreamNames extracts all signed stream names from an HTML page.
func ExtractSignedStreamNames(html string) map[string]string {
	result := make(map[string]string)

	matches := signedStreamRegex.FindAllStringSubmatch(html, -1)
	for _, match := range matches {
		if len(match) > 1 {
			signedName := match[1]
			channelType := decodeChannelType(signedName)
			if channelType != "" {
				result[channelType] = signedName
			}
		}
	}

	return result
}

// ExtractProfileInfo extracts profile information from a character page.
func ExtractProfileInfo(html string) (*Profile, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}

	profile := &Profile{}

	// Extract profile ID from form or data attributes
	doc.Find("[name='message[profile_id]']").Each(func(i int, s *goquery.Selection) {
		if val, exists := s.Attr("value"); exists {
			profile.ID, _ = strconv.ParseInt(val, 10, 64)
		}
	})

	// Extract name from title or header
	if title := doc.Find("h1, .profile-name, [data-profile-name]").First().Text(); title != "" {
		profile.Name = strings.TrimSpace(title)
	}

	// Extract profile image
	doc.Find(".profile-image img, [data-profile-image] img").Each(func(i int, s *goquery.Selection) {
		if src, exists := s.Attr("src"); exists {
			profile.ImageURL = src
		}
	})

	return profile, nil
}

// decodeChannelType decodes a signed stream name to extract the channel type.
func decodeChannelType(signedName string) string {
	// Split signature
	parts := strings.Split(signedName, "--")
	if len(parts) == 0 {
		return ""
	}

	decoded, err := decodeBase64(parts[0])
	if err != nil {
		return ""
	}

	// Format: "gid://candy-ai/Resource/ID:channel_type"
	if idx := strings.LastIndex(decoded, ":"); idx != -1 {
		// Remove quotes if present
		channelType := strings.Trim(decoded[idx+1:], `"`)
		return channelType
	}

	return ""
}

// decodeBase64 decodes a base64 string, handling URL-safe and standard encoding.
func decodeBase64(s string) (string, error) {
	// Try standard encoding first
	decoded, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		// Try URL-safe encoding
		decoded, err = base64.URLEncoding.DecodeString(s)
		if err != nil {
			// Try raw (no padding)
			decoded, err = base64.RawStdEncoding.DecodeString(s)
			if err != nil {
				return "", err
			}
		}
	}
	return string(decoded), nil
}

// cleanMessageBody cleans up extracted message text.
func cleanMessageBody(body string) string {
	// Remove excessive whitespace
	body = strings.Join(strings.Fields(body), " ")

	// Remove common UI text that might be captured
	uiText := []string{
		"Generate Image",
		"Copy",
		"Edit",
		"Regenerate",
	}

	for _, t := range uiText {
		body = strings.ReplaceAll(body, t, "")
	}

	return strings.TrimSpace(body)
}

// ParseConversationList parses a conversation list HTML.
func ParseConversationList(html string) ([]*Conversation, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}

	var conversations []*Conversation

	// Find conversation items
	doc.Find("[data-conversation-id], .conversation-item, [id^='conversation-']").Each(func(i int, s *goquery.Selection) {
		conv := &Conversation{}

		// Extract conversation ID
		if id, exists := s.Attr("data-conversation-id"); exists {
			conv.ID, _ = strconv.ParseInt(id, 10, 64)
		} else if id, exists := s.Attr("id"); exists {
			if matches := conversationIDRegex.FindStringSubmatch(id); len(matches) > 1 {
				conv.ID, _ = strconv.ParseInt(matches[1], 10, 64)
			}
		}

		// Extract profile info
		if profileLink := s.Find("a[href*='/ai-girlfriend/']").First(); profileLink.Length() > 0 {
			if href, exists := profileLink.Attr("href"); exists {
				parts := strings.Split(href, "/")
				if len(parts) > 0 {
					conv.ProfileSlug = parts[len(parts)-1]
				}
			}
		}

		// Extract name
		if name := s.Find(".profile-name, .conversation-name, h3, h4").First().Text(); name != "" {
			conv.ProfileName = strings.TrimSpace(name)
		}

		// Extract last message preview
		if preview := s.Find(".last-message, .message-preview, p").First().Text(); preview != "" {
			conv.LastMessage = strings.TrimSpace(preview)
		}

		// Extract profile image
		if img := s.Find("img").First(); img.Length() > 0 {
			if src, exists := img.Attr("src"); exists {
				conv.ProfileImageURL = src
			}
		}

		if conv.ID != 0 {
			conversations = append(conversations, conv)
		}
	})

	return conversations, nil
}

// ParseMessageHistory parses message history from a load.turbo_stream response.
func ParseMessageHistory(html string) ([]*Message, error) {
	actions, err := ParseTurboStream(html)
	if err != nil {
		return nil, err
	}

	var messages []*Message

	for _, action := range actions {
		if action.Action == "prepend" || action.Action == "append" {
			msg, err := ExtractMessageFromTurboStream(&action)
			if err == nil && msg != nil && msg.ID != 0 {
				messages = append(messages, msg)
			}
		}
	}

	return messages, nil
}
