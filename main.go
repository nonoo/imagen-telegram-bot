package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"golang.org/x/exp/slices"
)

const errorStr = "❌ Error"

var apiClient openai.Client
var telegramBot *bot.Bot

var cmdHandlers []*cmdHandlerType
var cmdHandlersMutex sync.Mutex

func uploadImages(ctx context.Context, replyToMsg *models.Message, description string, imgs [][]byte) (msg *models.Message, err error) {
	var media []models.InputMedia
	for i := range imgs {
		var c string
		if i == 0 {
			c = description
			if len(c) > 1024 {
				c = c[:1021] + "..."
			}
		}
		filename := fmt.Sprintf("imagen-%s.png", time.Now().Format("250423-213045"))
		media = append(media, &models.InputMediaPhoto{
			Media:           "attach://" + filename,
			MediaAttachment: bytes.NewReader(imgs[i]),
			Caption:         c,
		})
	}
	params := &bot.SendMediaGroupParams{
		ChatID:          replyToMsg.Chat.ID,
		MessageThreadID: replyToMsg.MessageThreadID,
		Media:           media,
	}
	_, err = telegramBot.SendMediaGroup(ctx, params)
	if err != nil {
		fmt.Println("  send images error:", err)
	}
	return
}

func sendMessage(ctx context.Context, chatID int64, s string) (msg *models.Message, err error) {
	msg, err = telegramBot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   s,
	})
	if err != nil {
		msg, err = telegramBot.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   s,
		})
		if err != nil {
			fmt.Println("  send error:", err)
			msg = nil
		}
	}
	return
}

func sendReplyToMessage(ctx context.Context, replyToMsg *models.Message, s string) (msg *models.Message, err error) {
	msg, err = telegramBot.SendMessage(ctx, &bot.SendMessageParams{
		ReplyParameters: &models.ReplyParameters{
			MessageID: replyToMsg.ID,
		},
		ChatID: replyToMsg.Chat.ID,
		Text:   s,
	})
	if err != nil {
		msg, err = telegramBot.SendMessage(ctx, &bot.SendMessageParams{
			ReplyParameters: &models.ReplyParameters{
				MessageID: replyToMsg.ID,
			},
			ChatID: replyToMsg.Chat.ID,
			Text:   s,
		})
		if err != nil {
			fmt.Println("  reply send error:", err)
			msg = replyToMsg
		}
	}
	return
}

func sendChatActionTyping(ctx context.Context, chatID int64) {
	action := bot.SendChatActionParams{
		ChatID: chatID,
		Action: models.ChatActionTyping,
	}

	_, err := telegramBot.SendChatAction(ctx, &action)
	if err != nil {
		fmt.Println("  send chat action error:", err)
	}
}

func sendTextToAdmins(ctx context.Context, s string) {
	for _, chatID := range params.AdminUserIDs {
		_, _ = sendMessage(ctx, chatID, s)
	}
}

func getMimeType(data []byte) (mimeType, extension string) {
	detectedMimeType := http.DetectContentType(data)

	switch detectedMimeType {
	case "image/png":
		extension = ".png"
	case "image/jpeg":
		extension = ".jpg"
	case "image/webp":
		extension = ".webp"
	default:
		detectedMimeType = "application/octet-stream"
		extension = ".bin"
	}

	return detectedMimeType, extension
}

func handleImageMessage(ctx context.Context, msg *models.Message) {
	var doc *models.Document
	if msg.Document != nil {
		doc = msg.Document
	} else if len(msg.Photo) > 0 {
		doc = &models.Document{
			FileID:   msg.Photo[len(msg.Photo)-1].FileID,
			FileName: msg.Photo[len(msg.Photo)-1].FileUniqueID,
		}
	} else {
		fmt.Println("  no document or photo")
		return
	}

	// Searching for the handler that is expecting image data.
	var cmdHandler *cmdHandlerType
	cmdHandlersMutex.Lock()
	for i, h := range cmdHandlers {
		if h.expectImageFromID == msg.From.ID && h.expectImageChan != nil {
			cmdHandler = cmdHandlers[i]
			break
		}
	}
	cmdHandlersMutex.Unlock()

	if cmdHandler == nil {
		fmt.Println("  no handler waiting for image data")
		return
	}

	f, err := telegramBot.GetFile(ctx, &bot.GetFileParams{
		FileID: doc.FileID,
	})
	if err != nil {
		fmt.Println("  can't get file:", err)
		_, _ = sendReplyToMessage(ctx, cmdHandler.cmdMsg, errorStr+": can't get file: "+err.Error())
		return
	}
	resp, err := http.Get("https://api.telegram.org/file/bot" + params.BotToken + "/" + f.FilePath)
	if err != nil {
		fmt.Println("  can't download file:", err)
		_, _ = sendReplyToMessage(ctx, cmdHandler.cmdMsg, errorStr+": can't download file: "+err.Error())
		return
	}

	d, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		fmt.Println("  can't read file:", err)
		_, _ = sendReplyToMessage(ctx, cmdHandler.cmdMsg, errorStr+": can't read file: "+err.Error())
		return
	}

	// Check if the filename already has an extension
	filename := doc.FileName
	if len(doc.FileName) == 0 {
		filename = "image"
	}

	mimeType, extension := getMimeType(d)
	if !strings.Contains(doc.FileName, ".") {
		// Add appropriate extension based on file content
		filename += extension
	}

	cmdHandler.expectImageChan <- ImageFilesDataType{
		Data:     d,
		Filename: filename,
		MimeType: mimeType,
	}
}

func handleMessage(ctx context.Context, update *models.Update) {
	fmt.Print("msg from ", update.Message.From.Username, "#", update.Message.From.ID, ": ", update.Message.Text, "\n")

	if update.Message.Chat.ID >= 0 { // From user?
		if !slices.Contains(params.AllowedUserIDs, update.Message.From.ID) {
			fmt.Println("  user not allowed, ignoring")
			return
		}
	} else { // From group ?
		fmt.Print("  msg from group #", update.Message.Chat.ID)
		if !slices.Contains(params.AllowedGroupIDs, update.Message.Chat.ID) {
			fmt.Println(", group not allowed, ignoring")
			return
		}
		fmt.Println()
	}

	cmdHandler := cmdHandlerType{
		cmdMsg: update.Message,
	}
	cmdHandlersMutex.Lock()
	cmdHandlers = append(cmdHandlers, &cmdHandler)
	cmdHandlersMutex.Unlock()

	defer func() {
		cmdHandlersMutex.Lock()
		for i, h := range cmdHandlers {
			if h == &cmdHandler {
				cmdHandlers = append(cmdHandlers[:i], cmdHandlers[i+1:]...)
				break
			}
		}
		cmdHandlersMutex.Unlock()
	}()

	// Check if message is a command.
	if update.Message.Text[0] == '/' || update.Message.Text[0] == '!' {
		cmd := strings.Split(update.Message.Text, " ")[0]
		if strings.Contains(cmd, "@") {
			cmd = strings.Split(cmd, "@")[0]
		}
		update.Message.Text = strings.TrimPrefix(update.Message.Text, cmd+" ")
		update.Message.Text = strings.TrimPrefix(update.Message.Text, cmd)
		cmdChar := string(cmd[0])
		cmd = cmd[1:] // Cutting the command character.
		switch cmd {
		case "imagen":
			fmt.Println("  interpreting as cmd imagen")
			cmdHandler.Imagen(ctx)
			return
		case "imagencancel":
			fmt.Println("  interpreting as cmd imagencancel")
			cmdHandler.Cancel(ctx)
			return
		case "imagenhelp":
			fmt.Println("  interpreting as cmd imagenhelp")
			cmdHandler.Help(ctx, cmdChar)
			return
		case "start":
			fmt.Println("  interpreting as cmd start")
			if update.Message.Chat.ID >= 0 { // From user?
				_, _ = sendReplyToMessage(ctx, update.Message, "🤖 Welcome! This is the Imagen Telegram Bot\n\n"+
					"More info: https://github.com/nonoo/imagen-telegram-bot")
			}
			return
		default:
			fmt.Println("  invalid cmd")
			if update.Message.Chat.ID >= 0 {
				_, _ = sendReplyToMessage(ctx, update.Message, errorStr+": invalid command")
			}
			return
		}
	}

	if update.Message.Chat.ID >= 0 {
		cmdHandler.Imagen(ctx)
	}
}

func telegramBotUpdateHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}

	if update.Message.Document != nil || len(update.Message.Photo) > 0 {
		handleImageMessage(ctx, update.Message)
	} else if update.Message.Text != "" {
		handleMessage(ctx, update)
	}
}

func main() {
	fmt.Println("imagen-telegram-bot starting...")

	if err := params.Init(); err != nil {
		fmt.Println("error:", err)
		os.Exit(1)
	}

	apiClient = openai.NewClient(option.WithAPIKey(params.OpenAIAPIKey))

	var cancel context.CancelFunc
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	typingHandler.Start(ctx)

	opts := []bot.Option{
		bot.WithDefaultHandler(telegramBotUpdateHandler),
	}

	var err error
	telegramBot, err = bot.New(params.BotToken, opts...)
	if nil != err {
		panic(fmt.Sprint("can't init telegram bot: ", err))
	}

	sendTextToAdmins(ctx, "🤖 Bot started")

	telegramBot.Start(ctx)
}
