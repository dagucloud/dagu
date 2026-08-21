// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package s3

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
)

// ListObject represents a single S3 object in list results.
type ListObject struct {
	Key          string `json:"key"`
	Size         int64  `json:"size"`
	LastModified string `json:"lastModified"`
	ETag         string `json:"etag,omitempty"`
	StorageClass string `json:"storageClass,omitempty"`
}

// ListResult contains the result of a list operation.
type ListResult struct {
	Operation  string       `json:"operation"`
	Success    bool         `json:"success"`
	Bucket     string       `json:"bucket"`
	Prefix     string       `json:"prefix,omitempty"`
	Objects    []ListObject `json:"objects"`
	TotalCount int          `json:"totalCount"`
	Duration   string       `json:"duration"`
}

func (e *executorImpl) runList(ctx context.Context) error {
	start := time.Now()

	maxObjects := e.cfg.MaxKeys
	if maxObjects <= 0 {
		maxObjects = 1000
	}

	// Stream mode outputs each object as a separate JSON line
	if e.cfg.OutputFormat == "jsonl" {
		return e.runListStream(ctx, maxObjects)
	}

	// Default mode collects all objects and returns a single JSON result
	var objects []ListObject
	err := e.visitObjects(ctx, e.cfg.Recursive, maxObjects, func(object ListObject) error {
		objects = append(objects, object)
		return nil
	})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrListFailed, err)
	}

	return e.writeResult(ListResult{
		Operation:  opList,
		Success:    true,
		Bucket:     e.cfg.Bucket,
		Prefix:     e.cfg.Prefix,
		Objects:    objects,
		TotalCount: len(objects),
		Duration:   time.Since(start).Round(time.Millisecond).String(),
	})
}

func (e *executorImpl) runListStream(ctx context.Context, maxObjects int) error {
	err := e.visitObjects(ctx, e.cfg.Recursive, maxObjects, func(object ListObject) error {
		if err := encodeJSON(e.stdout, object); err != nil {
			return fmt.Errorf("%w: failed to write output: %v", ErrListFailed, err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrListFailed, err)
	}
	return nil
}

func (e *executorImpl) visitObjects(ctx context.Context, recursive bool, maxObjects int, visit func(ListObject) error) error {
	input := &awss3.ListObjectsV2Input{
		Bucket: aws.String(e.cfg.Bucket),
		Prefix: aws.String(e.cfg.Prefix),
	}
	if !recursive {
		delimiter := e.cfg.Delimiter
		if delimiter == "" {
			delimiter = "/"
		}
		input.Delimiter = aws.String(delimiter)
	}

	paginator := awss3.NewListObjectsV2Paginator(e.client, input, func(options *awss3.ListObjectsV2PaginatorOptions) {
		if maxObjects > 0 && maxObjects < 1000 {
			options.Limit = int32(maxObjects)
		}
	})
	count := 0
	for paginator.HasMorePages() && (maxObjects <= 0 || count < maxObjects) {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return err
		}

		objects := make([]ListObject, 0, len(page.Contents)+len(page.CommonPrefixes))
		for _, object := range page.Contents {
			lastModified := ""
			if object.LastModified != nil {
				lastModified = object.LastModified.Format(time.RFC3339)
			}
			objects = append(objects, ListObject{
				Key:          aws.ToString(object.Key),
				Size:         aws.ToInt64(object.Size),
				LastModified: lastModified,
				ETag:         strings.Trim(aws.ToString(object.ETag), `"`),
				StorageClass: string(object.StorageClass),
			})
		}
		for _, prefix := range page.CommonPrefixes {
			objects = append(objects, ListObject{Key: aws.ToString(prefix.Prefix)})
		}
		sort.Slice(objects, func(i, j int) bool { return objects[i].Key < objects[j].Key })

		for _, object := range objects {
			if maxObjects > 0 && count >= maxObjects {
				return nil
			}
			if err := visit(object); err != nil {
				return err
			}
			count++
		}
	}
	return nil
}
