package main

import (
	"context"
	"slices"
	"time"
)

type TypingChange struct {
	ChatID    int64
	MessageID int
	IsTyping  bool
}

type typingHandlerType struct {
	ch        chan TypingChange
	typingIDs map[int]int64 // map[MessageID]ChatID
}

var typingHandler typingHandlerType

func (t *typingHandlerType) ChangeTypingStatus(chatID int64, messageID int, isTyping bool) {
	t.ch <- TypingChange{
		ChatID:    chatID,
		MessageID: messageID,
		IsTyping:  isTyping,
	}
}

func (t *typingHandlerType) Process(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case typingChange := <-t.ch:
			if typingChange.IsTyping {
				if _, exists := t.typingIDs[typingChange.MessageID]; !exists {
					t.typingIDs[typingChange.MessageID] = typingChange.ChatID
					sendChatActionTyping(ctx, typingChange.ChatID)
				}
			} else {
				delete(t.typingIDs, typingChange.MessageID)
			}
		case <-time.After(4 * time.Second):
			var sendTypingToChatIDs []int64
			for _, chatID := range t.typingIDs {
				if !slices.Contains(sendTypingToChatIDs, chatID) {
					sendTypingToChatIDs = append(sendTypingToChatIDs, chatID)
				}
			}
			for _, chatID := range sendTypingToChatIDs {
				sendChatActionTyping(ctx, chatID)
			}
		}
	}
}

func (c *typingHandlerType) Start(ctx context.Context) {
	c.ch = make(chan TypingChange)
	c.typingIDs = make(map[int]int64)
	go c.Process(ctx)
}
