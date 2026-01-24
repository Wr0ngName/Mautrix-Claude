// Package claudeapi provides a client for the Claude API.
package claudeapi

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// parseSSELine parses a single SSE line and returns an event if it's a data line.
func parseSSELine(line string) (*StreamEvent, error) {
	// Ignore empty lines
	if line == "" {
		return nil, nil
	}

	// Ignore comment lines
	if strings.HasPrefix(line, ":") {
		return nil, nil
	}

	// Ignore event: lines (we only care about data:)
	if strings.HasPrefix(line, "event:") {
		return nil, nil
	}

	// Parse data: lines
	if strings.HasPrefix(line, "data:") {
		data := strings.TrimPrefix(line, "data:")
		data = strings.TrimSpace(data)

		if data == "" {
			return nil, nil
		}

		var event StreamEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return nil, err
		}

		return &event, nil
	}

	// Ignore other lines
	return nil, nil
}

// streamMessages reads SSE events from an HTTP response and sends them on a channel.
func (c *Client) streamMessages(ctx context.Context, resp *http.Response, model string) (<-chan StreamEvent, error) {
	eventChan := make(chan StreamEvent, StreamEventBufferSize)

	go func() {
		defer close(eventChan)
		defer resp.Body.Close()

		startTime := time.Now()
		var inputTokens, outputTokens int

		scanner := bufio.NewScanner(resp.Body)

		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			default:
			}

			line := scanner.Text()
			event, err := parseSSELine(line)

			if err != nil {
				c.Log.Warn().Err(err).Str("line", line).Msg("Failed to parse SSE line")
				continue
			}

			if event != nil {
				// Track token usage from events
				if event.Type == "message_start" && event.Message != nil && event.Message.Usage != nil {
					inputTokens = event.Message.Usage.InputTokens
				}
				if event.Type == "message_delta" && event.Usage != nil {
					outputTokens = event.Usage.OutputTokens
				}

				select {
				case eventChan <- *event:
				case <-ctx.Done():
					return
				}
			}
		}

		if err := scanner.Err(); err != nil {
			c.Log.Warn().Err(err).Msg("Error reading SSE stream")
		}

		// Record metrics for the streaming request
		duration := time.Since(startTime)
		c.Metrics.RecordRequest(model, duration, inputTokens, outputTokens)
	}()

	return eventChan, nil
}
