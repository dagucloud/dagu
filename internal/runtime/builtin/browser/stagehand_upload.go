// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	stagehand "github.com/browserbase/stagehand/packages/sdk-go/v4"
	"github.com/dagucloud/dagu/v2/internal/browserhost"
)

// filePickerWait is how long an upload waits, after it clicks an element,
// for the file picker the click opens. A page may open it only after a
// request, within the seconds a click lets it.
const filePickerWait = 5 * time.Second

// fileInputTest is the XPath test for a file input, whatever the case of
// its type.
const fileInputTest = `input[translate(@type,'FILE','file')='file']`

// pageFileInputsExpression lists the file inputs of the page and of the
// frames it shows, each by the XPath the browser runtime finds it by.
const pageFileInputsExpression = `(() => {
	` + shownDocumentsFunction + `
	return shownDocuments(document).flatMap(({doc, path}) => [...doc.querySelectorAll("input[type=file i]")].map((input) => path + xpathOf(input)));
})()`

// uploadMIMETypes are the types of common business files, which a system's
// own table may lack.
var uploadMIMETypes = map[string]string{
	".csv":  "text/csv",
	".doc":  "application/msword",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".heic": "image/heic",
	".ppt":  "application/vnd.ms-powerpoint",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".tif":  "image/tiff",
	".tiff": "image/tiff",
	".txt":  "text/plain",
	".xls":  "application/vnd.ms-excel",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".zip":  "application/zip",
}

func (e *stagehandEngine) Upload(ctx context.Context, selector string, files []uploadFile, timeout time.Duration) (string, error) {
	return boundCall(ctx, timeout+callTimeoutSlack, func(ctx context.Context) (string, error) {
		page, err := e.page(ctx)
		if err != nil {
			return "", err
		}
		e.watch(ctx)
		how, err := e.upload(ctx, page, selector, files)
		if err == nil {
			// The page may start sending the files as soon as it has them.
			e.settle(ctx)
		}
		return how, err
	})
}

func (e *stagehandEngine) upload(ctx context.Context, page *stagehand.Page, selector string, files []uploadFile) (string, error) {
	if selector == "" {
		raw, err := page.Evaluate(ctx, pageFileInputsExpression)
		if err != nil {
			return "", err
		}
		var inputs []string
		if err := json.Unmarshal(raw, &inputs); err != nil {
			return "", fmt.Errorf("find the page's file inputs: %w", err)
		}
		switch {
		case len(inputs) == 0:
			return "", errNoFileInput
		case len(inputs) > 1:
			return "", errFileInputs{count: len(inputs)}
		}
		return uploadDirect, setInputFiles(ctx, page, "xpath="+inputs[0], files)
	}
	xpath := strings.HasPrefix(selector, "xpath=") || strings.HasPrefix(selector, "/")
	if xpath {
		own := selector + "/descendant-or-self::" + fileInputTest
		if count, err := page.Locator(own).Count(ctx); err == nil && count == 1 {
			return uploadDirect, setInputFiles(ctx, page, own, files)
		}
	}
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	clickErr := browserhost.ChooseFiles(ctx, e.handle.CDPURL, paths, filePickerWait, func(ctx context.Context) error {
		return page.Locator(selector).Click(ctx, nil)
	})
	switch {
	case clickErr == nil:
		return uploadPicker, nil
	case errors.Is(clickErr, browserhost.ErrSingleFileChooser):
		return "", errSingleFile{files: len(files)}
	}
	if xpath {
		// A drop area that opens no picker when clicked still holds the
		// file input its drops go to.
		nearest := selector + "/ancestor-or-self::*[descendant-or-self::" + fileInputTest + "][1]/descendant-or-self::" + fileInputTest
		if count, err := page.Locator(nearest).Count(ctx); err == nil && count == 1 {
			return uploadNearby, setInputFiles(ctx, page, nearest, files)
		}
	}
	if errors.Is(clickErr, browserhost.ErrNoFileChooser) {
		return "", errors.New("clicking the element opened no file picker, and no single file input is near it; " +
			"with into, name the file input or the button that opens the file picker")
	}
	return "", fmt.Errorf("click the element to open the file picker: %w", clickErr)
}

// setInputFiles gives files to the first file input selector, an XPath,
// matches.
func setInputFiles(ctx context.Context, page *stagehand.Page, selector string, files []uploadFile) error {
	if len(files) > 1 {
		multiple, err := page.Locator(selector + "[@multiple]").Count(ctx)
		if err != nil {
			return err
		}
		if multiple == 0 {
			return errSingleFile{files: len(files)}
		}
	}
	inputs := make([]stagehand.FileInput, 0, len(files))
	for _, file := range files {
		data, err := os.ReadFile(file.Path)
		if err != nil {
			return err
		}
		input := stagehand.FileData(filepath.Base(file.Path), uploadMIMEType(file.Path), data)
		modified := file.ModTime.UnixMilli()
		input.LastModified = &modified
		inputs = append(inputs, input)
	}
	return page.Locator(selector).First().SetInputFiles(ctx, inputs...)
}

// uploadMIMEType is the media type a browser gives a file at path, or ""
// when it is unknown.
func uploadMIMEType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if known, ok := uploadMIMETypes[ext]; ok {
		return known
	}
	mediaType, _, err := mime.ParseMediaType(mime.TypeByExtension(ext))
	if err != nil {
		return ""
	}
	return mediaType
}
