package smee

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

type Client struct {
	ChannelURL string
	TargetURL  string
	httpClient *http.Client
}

type Event struct {
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	Query   string            `json:"query"`
}

func NewClient(channelURL, targetURL string) *Client {
	return &Client{
		ChannelURL: channelURL,
		TargetURL:  targetURL,
		httpClient: &http.Client{
			Timeout: 0,
		},
	}
}

func (c *Client) Start() error {
	eventsURL := strings.TrimSuffix(c.ChannelURL, "/") + "/events"

	log.Info().
		Str("channel", c.ChannelURL).
		Str("target", c.TargetURL).
		Msg("Starting smee client")

	for {
		if err := c.poll(eventsURL); err != nil {
			log.Error().Err(err).Msg("Smee poll error, retrying in 5s")
			time.Sleep(5 * time.Second)
		}
	}
}

func (c *Client) poll(eventsURL string) error {
	resp, err := c.httpClient.Get(eventsURL)
	if err != nil {
		return fmt.Errorf("failed to connect to smee: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("smee returned status %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "ready" {
			log.Info().Msg("Smee connection ready")
			continue
		}

		var event Event
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			log.Error().Err(err).Msg("Failed to parse smee event")
			continue
		}

		if err := c.forwardEvent(event); err != nil {
			log.Error().Err(err).Msg("Failed to forward event")
		}
	}

	return scanner.Err()
}

func (c *Client) forwardEvent(event Event) error {
	req, err := http.NewRequest(http.MethodPost, c.TargetURL, strings.NewReader(event.Body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	for key, value := range event.Headers {
		req.Header.Set(key, value)
	}

	if event.Query != "" {
		req.URL.RawQuery = event.Query
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to forward event: %w", err)
	}
	defer resp.Body.Close()

	io.Copy(io.Discard, resp.Body)

	log.Info().
		Str("status", resp.Status).
		Msg("Event forwarded")

	return nil
}
