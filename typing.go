package main

import (
	"context"
	"slices"
	"time"
)

type TypingChange struct {
	ChatID   int64
	IsTyping bool
}

type typingHandlerType struct {
	ch            chan TypingChange
	typingOnChats []int64
}

var typingHandler typingHandlerType

func (c *typingHandlerType) ChangeTypingStatus(chatID int64, isTyping bool) {
	c.ch <- TypingChange{
		ChatID:   chatID,
		IsTyping: isTyping,
	}
}

func (c *typingHandlerType) Process(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case typingChange := <-c.ch:
			if typingChange.IsTyping {
				if !slices.Contains(c.typingOnChats, typingChange.ChatID) {
					c.typingOnChats = append(c.typingOnChats, typingChange.ChatID)
					sendChatActionTyping(ctx, typingChange.ChatID)
				}
			} else {
				for i, chatID := range c.typingOnChats {
					if chatID == typingChange.ChatID {
						c.typingOnChats = append(c.typingOnChats[:i], c.typingOnChats[i+1:]...)
						break
					}
				}
			}
		case <-time.After(5 * time.Second):
			for _, chatID := range c.typingOnChats {
				sendChatActionTyping(ctx, chatID)
			}
		}
	}
}

func (c *typingHandlerType) Start(ctx context.Context) {
	c.ch = make(chan TypingChange)
	c.typingOnChats = []int64{}
	go c.Process(ctx)
}
