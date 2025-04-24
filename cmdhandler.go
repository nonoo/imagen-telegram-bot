package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/openai/openai-go"
)

type cmdHandlerType struct {
	sendTypingEnd chan bool
}

func (c *cmdHandlerType) reply(ctx context.Context, msg *models.Message, text string) (replyMsg *models.Message, err error) {
	if msg == nil {
		return sendMessage(ctx, msg.Chat.ID, text)
	}
	return sendReplyToMessage(ctx, msg, text)
}

func (c *cmdHandlerType) editReply(ctx context.Context, msg *models.Message, replyMsg *models.Message, text string) (replyMessage *models.Message, err error) {
	if replyMsg == nil || msg == nil {
		return c.reply(ctx, msg, text)
	}

	return editReplyToMessage(ctx, replyMsg, text)
}

func (c *cmdHandlerType) sendTyping(ctx context.Context, chatID int64) {
	for {
		sendChatActionTyping(ctx, chatID)
		select {
		case <-c.sendTypingEnd:
			return
		case <-time.After(5 * time.Second):
			// Continue the loop and send another typing action
		}
	}
}

func (c *cmdHandlerType) Imagen(ctx context.Context, msg *models.Message) {
	chatID := msg.Chat.ID

	// Parse command arguments
	var argsPresent []string
	isEdit := false
	size := "1024x1024"
	background := "opaque"
	quality := "auto"
	promptParts := []string{}

	// Split text into words
	words := strings.Fields(msg.Text)
	i := 0

	// Parse arguments
	for i < len(words) {
		word := words[i]

		if strings.HasPrefix(word, "-") {
			argName := strings.TrimPrefix(word, "-")

			switch argName {
			case "edit":
				argsPresent = append(argsPresent, "edit")
				isEdit = true
			case "size", "background", "quality":
				if i+1 >= len(words) || strings.HasPrefix(words[i+1], "-") {
					fmt.Println("	Missing value for flag:", argName)
					_, _ = c.reply(ctx, msg, errorStr+": Missing value for flag: "+argName)
					return
				}

				argsPresent = append(argsPresent, argName)

				value := words[i+1]
				i++ // Skip the next word as we've processed it

				switch argName {
				case "size":
					size = value
				case "background":
					background = value
				case "quality":
					quality = value
				}
			}
		} else {
			// Not a flag, add to prompt
			promptParts = append(promptParts, word)
		}

		i++
	}

	// Combine prompt parts into the final prompt
	prompt := strings.Join(promptParts, " ")

	fmt.Println("    parsed args: edit:", isEdit, "size:", size, "background:", background, "quality:", quality, "prompt:", prompt)

	c.sendTypingEnd = make(chan bool)
	go c.sendTyping(ctx, chatID)

	res, err := apiClient.Images.Generate(ctx, openai.ImageGenerateParams{
		Prompt:  prompt,
		Model:   "gpt-image-1",
		Size:    "1024x1024",
		Quality: "auto",
		// Background: background, TODO: not yet supported by the OpenAI Go library
	})

	c.sendTypingEnd <- true

	if err != nil {
		fmt.Println("    generate error:", err)
		_, _ = c.reply(ctx, msg, errorStr+": "+err.Error())
		return
	}

	// Decode base64 image data to bytes
	imgBytes, err := base64.StdEncoding.DecodeString(res.Data[0].B64JSON)
	if err != nil {
		fmt.Println("    base64 decode error:", err)
		_, _ = c.reply(ctx, msg, errorStr+": "+err.Error())
		return
	}

	// Create a slice of byte slices as required by uploadImages
	imgs := [][]byte{imgBytes}

	// Create a description for the image
	description := "💭 " + prompt
	if len(argsPresent) > 0 {
		argsDesc := ""
		for _, arg := range argsPresent {
			if argsDesc != "" {
				argsDesc += " "
			}

			switch arg {
			case "size":
				argsDesc += "Size: " + size
			case "background":
				argsDesc += "Background: " + background
			case "quality":
				argsDesc += "Quality: " + quality
			}
		}
		description += "\n🖼️ " + argsDesc
	}

	_, err = uploadImages(ctx, msg, description, imgs)
	if err != nil {
		fmt.Println("    upload error:", err)
		_, _ = c.reply(ctx, msg, errorStr+": "+err.Error())
		return
	}
}

func (c *cmdHandlerType) Help(ctx context.Context, msg *models.Message, cmdChar string) {
	_, _ = sendReplyToMessage(ctx, msg, "🤖 Imagen Telegram Bot\n\n"+
		"Available commands:\n\n"+
		cmdChar+"imagen (-edit) (-size 1024x1024) (-background transparent) (-quality auto) [prompt]\n"+
		cmdChar+"imagenhelp - show this help\n\n"+
		"For more information see https://github.com/nonoo/imagen-telegram-bot")
}
