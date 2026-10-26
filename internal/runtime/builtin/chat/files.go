// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package chat

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/dagucloud/dagu/v2/internal/cmn/document"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/filerefs"
)

const (
	// maxFilesRunes bounds the text of all the files a step gives the model.
	maxFilesRunes = 1_000_000
	// maxFilesImages bounds the images, image files and scanned pages
	// together, a step gives the model.
	maxFilesImages = 50
)

// attachFiles reads the files with.files names and gives them to the
// step's last user message: their text ahead of the prompt, and their
// images with it. Images go with that one request and are not kept with
// the run; the text is, so a later step of the session still reads it.
func attachFiles(ctx context.Context, files []string, messages []ir.LLMMessage) error {
	if len(files) == 0 {
		return nil
	}
	prompt := -1
	for i := range messages {
		if messages[i].Role == ir.LLMRoleUser {
			prompt = i
		}
	}
	if prompt < 0 {
		return fmt.Errorf("files needs a user prompt to go with")
	}
	entries := make([]string, 0, len(files))
	for _, entry := range files {
		resolved, err := runtime.ResolveString(ctx, entry, cmnvalue.WorkflowField("llm.files"))
		if err != nil {
			return fmt.Errorf("files: %w", err)
		}
		entries = append(entries, resolved)
	}
	paths, err := filerefs.Paths(entries, runtime.GetEnv(ctx).WorkingDir)
	if err != nil {
		return err
	}
	var text strings.Builder
	var images []ir.LLMImage
	for _, path := range paths {
		file, err := document.Read(ctx, path)
		if err != nil {
			return err
		}
		images = describeFile(&text, file, images)
		logger.Info(ctx, "Read a file for the model",
			slog.String("file", path),
			slog.Int("pages", len(file.Pages)),
			slog.Int("images", len(file.Images())))
	}
	if n := utf8.RuneCountInString(text.String()); n > maxFilesRunes {
		return fmt.Errorf("the files hold %d characters of text; a model step takes up to %d, so give it fewer or smaller files", n, maxFilesRunes)
	}
	if len(images) > maxFilesImages {
		return fmt.Errorf("the files hold %d images, image files and scanned pages together; a model step takes up to %d", len(images), maxFilesImages)
	}
	messages[prompt].Content = text.String() + messages[prompt].Content
	messages[prompt].Images = images
	return nil
}

// describeFile writes a file's text, naming its images by their place
// among the images sent, which it adds to.
func describeFile(text *strings.Builder, file document.File, images []ir.LLMImage) []ir.LLMImage {
	attach := func(image document.Image) int {
		images = append(images, ir.LLMImage{MediaType: image.MediaType, Data: image.Data})
		return len(images)
	}
	switch {
	case file.Image != nil:
		fmt.Fprintf(text, "[Image %d is %s.]\n\n", attach(*file.Image), file.Name)
	case file.Pages != nil:
		fmt.Fprintf(text, "<file name=%q pages=\"%d\">\n", file.Name, len(file.Pages))
		for i, page := range file.Pages {
			if page.Image != nil {
				fmt.Fprintf(text, "--- page %d: image %d ---\n", i+1, attach(*page.Image))
				continue
			}
			fmt.Fprintf(text, "--- page %d ---\n%s\n", i+1, strings.TrimRight(page.Text, "\r\n"))
		}
		text.WriteString("</file>\n\n")
	default:
		fmt.Fprintf(text, "<file name=%q>\n%s\n</file>\n\n", file.Name, strings.TrimRight(file.Text, "\r\n"))
	}
	return images
}

// withoutImages returns the messages without their images, as they are
// kept with the run.
func withoutImages(messages []ir.LLMMessage) []ir.LLMMessage {
	if messages == nil {
		return nil
	}
	kept := make([]ir.LLMMessage, len(messages))
	for i, message := range messages {
		message.Images = nil
		kept[i] = message
	}
	return kept
}
