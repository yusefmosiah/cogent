package native

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var ErrChannelClosed = errors.New("agent channel closed")

type ChannelMessage struct {
	From      string    `json:"from"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

type ChannelManager struct {
	mu       sync.Mutex
	channels map[string]*AgentChannel
}

func NewChannelManager() *ChannelManager {
	return &ChannelManager{channels: map[string]*AgentChannel{}}
}

func (m *ChannelManager) Channel(workID string) (*AgentChannel, error) {
	workID = strings.TrimSpace(workID)
	if workID == "" {
		return nil, fmt.Errorf("work_id must not be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if ch, ok := m.channels[workID]; ok {
		return ch, nil
	}
	ch := NewAgentChannel()
	m.channels[workID] = ch
	return ch, nil
}

func (m *ChannelManager) Close(workID string) error {
	ch, err := m.Channel(workID)
	if err != nil {
		return err
	}
	ch.Close()
	return nil
}

type AgentChannel struct {
	mu       sync.Mutex
	messages []ChannelMessage
	closed   bool
	waitCh   chan struct{}
}

func NewAgentChannel() *AgentChannel {
	return &AgentChannel{waitCh: make(chan struct{})}
}

func (c *AgentChannel) Cursor() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return uint64(len(c.messages))
}

func (c *AgentChannel) Post(message ChannelMessage) (uint64, error) {
	if strings.TrimSpace(message.Content) == "" {
		return 0, fmt.Errorf("message content must not be empty")
	}
	if message.Timestamp.IsZero() {
		message.Timestamp = time.Now().UTC()
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return 0, ErrChannelClosed
	}
	c.messages = append(c.messages, message)
	cursor := uint64(len(c.messages))
	waitCh := c.waitCh
	c.waitCh = make(chan struct{})
	c.mu.Unlock()

	close(waitCh)
	return cursor, nil
}

func (c *AgentChannel) ReadSince(cursor uint64) ([]ChannelMessage, uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if cursor > uint64(len(c.messages)) {
		return nil, uint64(len(c.messages)), fmt.Errorf("cursor %d out of range", cursor)
	}
	return cloneMessages(c.messages[cursor:]), uint64(len(c.messages)), nil
}

func (c *AgentChannel) Wait(ctx context.Context, cursor uint64) ([]ChannelMessage, uint64, error) {
	for {
		c.mu.Lock()
		switch {
		case cursor > uint64(len(c.messages)):
			next := uint64(len(c.messages))
			c.mu.Unlock()
			return nil, next, fmt.Errorf("cursor %d out of range", cursor)
		case cursor < uint64(len(c.messages)):
			messages := cloneMessages(c.messages[cursor:])
			next := uint64(len(c.messages))
			c.mu.Unlock()
			return messages, next, nil
		case c.closed:
			next := uint64(len(c.messages))
			c.mu.Unlock()
			return nil, next, ErrChannelClosed
		}
		waitCh := c.waitCh
		c.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, cursor, ctx.Err()
		case <-waitCh:
		}
	}
}

func (c *AgentChannel) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	waitCh := c.waitCh
	c.waitCh = make(chan struct{})
	c.mu.Unlock()

	close(waitCh)
}

func cloneMessages(messages []ChannelMessage) []ChannelMessage {
	if len(messages) == 0 {
		return []ChannelMessage{}
	}
	cloned := make([]ChannelMessage, len(messages))
	copy(cloned, messages)
	return cloned
}
