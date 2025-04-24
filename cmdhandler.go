package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"golang.org/x/exp/slices"
)

type ImageFilesDataType struct {
	Data     []byte
	Filename string
	MimeType string
}

type cmdHandlerType struct {
	cmdMsg          *models.Message
	expectImageChan chan ImageFilesDataType
}

func (c *cmdHandlerType) reply(ctx context.Context, text string) (replyMsg *models.Message, err error) {
	if c.cmdMsg == nil {
		return sendMessage(ctx, c.cmdMsg.Chat.ID, text)
	}
	return sendReplyToMessage(ctx, c.cmdMsg, text)
}

func (c *cmdHandlerType) ImagenResultProcess(ctx context.Context, res *openai.ImagesResponse, argsPresent []string, n int, prompt, size, background, quality string) {
	// Decode base64 image data to bytes
	imgBytes, err := base64.StdEncoding.DecodeString(res.Data[0].B64JSON)
	if err != nil {
		fmt.Println("    base64 decode error:", err)
		_, _ = c.reply(ctx, errorStr+": "+err.Error())
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

	_, err = uploadImages(ctx, c.cmdMsg, description, imgs)
	if err != nil {
		fmt.Println("    upload error:", err)
		_, _ = c.reply(ctx, errorStr+": "+err.Error())
		return
	}
}

var quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"")

func escapeQuotes(s string) string {
	return quoteEscaper.Replace(s)
}

func (c *cmdHandlerType) createMultipartBody(imgs []ImageFilesDataType, argsPresent []string, n int, prompt, size, background, quality string) (body []byte, contentType string, err error) {
	// Create multipart writer
	var b strings.Builder
	w := multipart.NewWriter(&b)

	// Add images
	for _, img := range imgs {
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="image[]"; filename="%s"`, escapeQuotes(img.Filename)))
		h.Set("Content-Type", img.MimeType)
		imgPart, err := w.CreatePart(h)
		if err != nil {
			return nil, "", err
		}
		_, err = imgPart.Write(img.Data)
		if err != nil {
			return nil, "", err
		}
	}

	// Add prompt
	promptPart, err := w.CreateFormField("prompt")
	if err != nil {
		return nil, "", err
	}
	_, err = promptPart.Write([]byte(prompt))
	if err != nil {
		return nil, "", err
	}

	// Add model
	modelPart, err := w.CreateFormField("model")
	if err != nil {
		return nil, "", err
	}
	_, err = modelPart.Write([]byte("gpt-image-1"))
	if err != nil {
		return nil, "", err
	}

	if slices.Contains(argsPresent, "n") {
		// Add n
		nPart, err := w.CreateFormField("n")
		if err != nil {
			return nil, "", err
		}
		_, err = nPart.Write([]byte(strconv.FormatInt(int64(n), 10)))
		if err != nil {
			return nil, "", err
		}
	}

	if slices.Contains(argsPresent, "size") {
		// Add size
		sizePart, err := w.CreateFormField("size")
		if err != nil {
			return nil, "", err
		}
		_, err = sizePart.Write([]byte(size))
		if err != nil {
			return nil, "", err
		}
	}

	if slices.Contains(argsPresent, "quality") {
		// Add quality
		qualityPart, err := w.CreateFormField("quality")
		if err != nil {
			return nil, "", err
		}
		_, err = qualityPart.Write([]byte(quality))
		if err != nil {
			return nil, "", err
		}
	}

	if slices.Contains(argsPresent, "background") {
		// Add background
		bgPart, err := w.CreateFormField("background")
		if err != nil {
			return nil, "", err
		}
		_, err = bgPart.Write([]byte(background))
		if err != nil {
			return nil, "", err
		}
	}

	w.Close()

	return []byte(b.String()), w.FormDataContentType(), nil
}

func (c *cmdHandlerType) ImagenEdit(ctx context.Context, argsPresent []string, n int, prompt, size, background, quality string) {
	fmt.Println("    waiting for image data...")
	c.expectImageChan = make(chan ImageFilesDataType)
	_, _ = c.reply(ctx, "🩻 Please post the image file(s) to process.")

	var err error
	var imgs []ImageFilesDataType
	select {
	case img := <-c.expectImageChan:
		imgs = append(imgs, img)

		// Wait for more images or timeout
	waitForMultipleImages:
		for {
			select {
			case img := <-c.expectImageChan:
				imgs = append(imgs, img)
			case <-ctx.Done():
				err = fmt.Errorf("context done")
				break waitForMultipleImages
			case <-time.NewTimer(1 * time.Second).C:
				break waitForMultipleImages
			}
		}
	case <-ctx.Done():
		err = fmt.Errorf("context done")
	case <-time.NewTimer(3 * time.Minute).C:
		err = fmt.Errorf("waiting for image data timeout")
	}
	close(c.expectImageChan)
	c.expectImageChan = nil

	if err == nil && len(imgs) == 0 {
		err = fmt.Errorf("got no image data")
	}

	if err != nil {
		fmt.Println("    error:", err)
		_, _ = c.reply(ctx, errorStr+": "+err.Error())
		return
	}

	fmt.Println("    got", len(imgs), "images")

	typingHandler.ChangeTypingStatus(c.cmdMsg.Chat.ID, true)

	body, contentType, err := c.createMultipartBody(imgs, argsPresent, n, prompt, size, background, quality)
	if err != nil {
		fmt.Println("    create multipart body error:", err)
		_, _ = c.reply(ctx, errorStr+": "+err.Error())
		return
	}

	var res openai.ImagesResponse
	err = apiClient.Post(ctx, "images/edits", body, &res, option.WithHeader("Content-Type", contentType))

	typingHandler.ChangeTypingStatus(c.cmdMsg.Chat.ID, false)

	if err != nil {
		fmt.Println("    edit error:", err)
		_, _ = c.reply(ctx, errorStr+": "+err.Error())
		return
	}

	c.ImagenResultProcess(ctx, &res, argsPresent, n, prompt, size, background, quality)
}

type ImageGenerateParams struct {
	Prompt     string `json:"prompt"`
	N          int64  `json:"n,omitzero"`
	Model      string `json:"model,omitzero"`
	Size       string `json:"size,omitzero"`
	Quality    string `json:"quality,omitzero"`
	Background string `json:"background,omitzero"`
}

func (c *cmdHandlerType) ImagenGenerate(ctx context.Context, argsPresent []string, n int, prompt, size, background, quality string) {
	typingHandler.ChangeTypingStatus(c.cmdMsg.Chat.ID, true)

	parms := ImageGenerateParams{
		Prompt:     prompt,
		N:          int64(n),
		Model:      "gpt-image-1",
		Size:       size,
		Quality:    quality,
		Background: background,
	}
	body, err := json.Marshal(parms)
	if err != nil {
		fmt.Println("    json marshal error:", err)
		_, _ = c.reply(ctx, errorStr+": "+err.Error())
		return
	}

	var res openai.ImagesResponse
	err = apiClient.Post(ctx, "images/generations", body, &res, option.WithHeader("Content-Type", "application/json"))

	typingHandler.ChangeTypingStatus(c.cmdMsg.Chat.ID, false)

	if err != nil {
		fmt.Println("    generate error:", err)
		_, _ = c.reply(ctx, errorStr+": "+err.Error())
		return
	}

	c.ImagenResultProcess(ctx, &res, argsPresent, n, prompt, size, background, quality)
}

func (c *cmdHandlerType) Imagen(ctx context.Context) {
	// Parse command arguments
	var argsPresent []string
	isEdit := false
	n := 1
	size := string(openai.ImageEditParamsSize1024x1024)
	background := "opaque"
	quality := "auto"
	promptParts := []string{}

	// Split text into words
	words := strings.Fields(c.cmdMsg.Text)
	i := 0

	// Parse arguments
	for i < len(words) {
		word := words[i]

		if strings.HasPrefix(word, "-") {
			argName := strings.TrimPrefix(word, "-")

			switch argName {
			case "edit":
				isEdit = true
			case "n", "size", "background", "quality":
				if i+1 >= len(words) || strings.HasPrefix(words[i+1], "-") {
					fmt.Println("	Missing value for flag:", argName)
					_, _ = c.reply(ctx, errorStr+": Missing value for flag: "+argName)
					return
				}

				argsPresent = append(argsPresent, argName)

				value := words[i+1]
				i++ // Skip the next word as we've processed it

				switch argName {
				case "n":
					var err error
					n, err = strconv.Atoi(value)
					if err != nil {
						fmt.Println("	Invalid value for n:", value)
						_, _ = c.reply(ctx, errorStr+": Invalid value for n: "+value)
						return
					}
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
	prompt = strings.TrimSpace(prompt)

	if prompt == "" {
		fmt.Println("	No prompt provided")
		_, _ = c.reply(ctx, errorStr+": No prompt provided")
		return
	}

	fmt.Println("    parsed args: n:", n, "edit:", isEdit, "size:", size, "background:", background, "quality:", quality, "prompt:", prompt)

	if isEdit {
		c.ImagenEdit(ctx, argsPresent, n, prompt, size, background, quality)
		return
	}
	c.ImagenGenerate(ctx, argsPresent, n, prompt, size, background, quality)
}

func (c *cmdHandlerType) Help(ctx context.Context, cmdChar string) {
	_, _ = sendReplyToMessage(ctx, c.cmdMsg, "🤖 Imagen Telegram Bot\n\n"+
		"Available commands:\n\n"+
		cmdChar+"imagen (args) [prompt]\n"+
		"  args can be:\n"+
		"    -edit: toggles edit mode\n"+
		"    -n 1: generate n output images\n"+
		"    -size 1024x1024\n"+
		"    -background transparent (default is opaque)\n"+
		"    -quality auto\n"+
		cmdChar+"imagenhelp - show this help\n\n"+
		"For more information see https://github.com/nonoo/imagen-telegram-bot and https://platform.openai.com/docs/guides/image-generation")
}
