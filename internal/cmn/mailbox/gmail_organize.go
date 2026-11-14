// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mailbox

import (
	"cmp"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"google.golang.org/api/gmail/v1"
)

// gmailBatchLimit is the most emails one batchModify call changes.
const gmailBatchLimit = 1000

type gmailItem struct {
	id   string
	ref  gmailRef
	dest string
}

// Organize applies opts to its items. Items are checked before anything
// changes; a failure partway leaves earlier changes applied.
func (g *Gmail) Organize(opts OrganizeOptions) (*OrganizeResult, error) {
	items := make([]gmailItem, 0, len(opts.Items))
	for _, item := range opts.Items {
		ref, err := parseGmailID(item.ID)
		if err != nil {
			return nil, fmt.Errorf("%w: %q", err, item.ID)
		}
		dest := ""
		if opts.Move == MoveFolder {
			if dest = cmp.Or(item.MoveTo, opts.Folder); dest == "" {
				return nil, errors.New("move to folder needs a folder for every email")
			}
		}
		items = append(items, gmailItem{id: item.ID, ref: ref, dest: dest})
	}

	result := &OrganizeResult{Missing: []string{}}
	present, err := g.presentItems(items, result)
	if err != nil {
		return result, err
	}
	if len(present) == 0 || opts.DryRun {
		result.Changed = len(present)
		return result, nil
	}
	if err := g.mark(present, opts.Mark); err != nil {
		return result, err
	}
	if err := g.move(present, opts.Move); err != nil {
		return result, err
	}
	result.Changed = len(present)
	return result, nil
}

// presentItems keeps the items whose email is still where it was found,
// recording the rest as missing.
func (g *Gmail) presentItems(items []gmailItem, result *OrganizeResult) ([]gmailItem, error) {
	var present []gmailItem
	for _, item := range items {
		found, err := g.users.Messages.Get(gmailUser, item.ref.message).Format("minimal").Context(g.ctx).Do()
		if gmailStatus(err) == http.StatusNotFound {
			result.Missing = append(result.Missing, item.id)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("check emails: %w", gmailError(err))
		}
		if !item.ref.present(found.LabelIds) {
			result.Missing = append(result.Missing, item.id)
			continue
		}
		present = append(present, item)
	}
	return present, nil
}

func (g *Gmail) mark(items []gmailItem, mark string) error {
	var add, remove []string
	switch mark {
	case "":
		return nil
	case MarkRead:
		remove = []string{labelUnread}
	case MarkUnread:
		add = []string{labelUnread}
	case MarkFlagged:
		add = []string{labelStarred}
	case MarkUnflagged:
		remove = []string{labelStarred}
	default:
		return fmt.Errorf("unknown mark %q", mark)
	}
	if err := g.modify(items, add, remove); err != nil {
		return fmt.Errorf("mark emails %s: %w", mark, err)
	}
	return nil
}

func (g *Gmail) move(items []gmailItem, move string) error {
	switch move {
	case "":
		return nil
	case MoveTrash:
		return g.trash(items)
	case MoveArchive:
		// Archiving takes away the label the email was found under, as a move
		// to All Mail does over IMAP.
		return g.relabel(items, func(gmailItem) (string, error) { return "", nil })
	case MoveFolder:
		return g.relabel(items, func(item gmailItem) (string, error) { return g.folderLabel(item.dest) })
	default:
		return fmt.Errorf("unknown move %q", move)
	}
}

// relabel moves each item from the label it was found under to the label
// destination names for it. Moving to All Mail, the empty label, only takes
// the old label away.
func (g *Gmail) relabel(items []gmailItem, destination func(gmailItem) (string, error)) error {
	type route struct{ from, to string }
	var routes []route
	byRoute := map[route][]gmailItem{}
	for _, item := range items {
		to, err := destination(item)
		if err != nil {
			return err
		}
		r := route{from: item.ref.label, to: to}
		if r.from == r.to {
			continue
		}
		if _, seen := byRoute[r]; !seen {
			routes = append(routes, r)
		}
		byRoute[r] = append(byRoute[r], item)
	}

	for _, r := range routes {
		// Gmail moves email to the trash only through its own call.
		if r.to == labelTrash {
			if err := g.trash(byRoute[r]); err != nil {
				return err
			}
			continue
		}
		var add, remove []string
		if r.to != "" {
			add = []string{r.to}
		}
		if r.from != "" {
			remove = []string{r.from}
		}
		if err := g.modify(byRoute[r], add, remove); err != nil {
			return fmt.Errorf("move emails: %w", err)
		}
	}
	return nil
}

func (g *Gmail) trash(items []gmailItem) error {
	for _, item := range items {
		if item.ref.label == labelTrash {
			continue
		}
		_, err := g.users.Messages.Trash(gmailUser, item.ref.message).Context(g.ctx).Do()
		if err != nil && gmailStatus(err) != http.StatusNotFound {
			return fmt.Errorf("move emails to the trash: %w", gmailError(err))
		}
	}
	return nil
}

func (g *Gmail) modify(items []gmailItem, add, remove []string) error {
	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = item.ref.message
	}
	for chunk := range slices.Chunk(ids, gmailBatchLimit) {
		request := &gmail.BatchModifyMessagesRequest{Ids: chunk, AddLabelIds: add, RemoveLabelIds: remove}
		if err := g.users.Messages.BatchModify(gmailUser, request).Context(g.ctx).Do(); err != nil {
			return gmailError(err)
		}
	}
	return nil
}
