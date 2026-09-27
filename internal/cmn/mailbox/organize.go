// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mailbox

import (
	"errors"
	"fmt"
	"strings"

	"github.com/emersion/go-imap/v2"
)

// Marks for OrganizeOptions.Mark.
const (
	MarkRead      = "read"
	MarkUnread    = "unread"
	MarkFlagged   = "flagged"
	MarkUnflagged = "unflagged"
)

// Moves for OrganizeOptions.Move.
const (
	MoveFolder  = "folder"
	MoveArchive = "archive"
	MoveTrash   = "trash"
)

// Item names an email to organize. MoveTo overrides OrganizeOptions.Folder
// when moving to a folder.
type Item struct {
	ID     string
	MoveTo string
}

// OrganizeOptions describes what to do with each item: mark, then move.
type OrganizeOptions struct {
	Items  []Item
	Mark   string
	Move   string
	Folder string
	DryRun bool
}

// OrganizeResult reports how many emails changed, or would change in a dry
// run, and the IDs of emails no longer in their folder.
type OrganizeResult struct {
	Changed int
	Missing []string
}

// ErrUnsafeMove means the server can move email only by expunging every email
// marked deleted in the folder, which could remove mail this action never saw.
var ErrUnsafeMove = errors.New("IMAP server supports neither MOVE nor UIDPLUS, so email cannot be moved safely")

type pendingItem struct {
	id   string
	ref  emailRef
	dest string
}

// Organize applies opts to its items. Items are checked before anything
// changes; a failure partway leaves earlier changes applied.
func (c *Client) Organize(opts OrganizeOptions) (*OrganizeResult, error) {
	var folders []string
	byFolder := map[string][]pendingItem{}
	for _, item := range opts.Items {
		ref, err := parseID(item.ID)
		if err != nil {
			return nil, fmt.Errorf("%w: %q", err, item.ID)
		}
		dest := ""
		if opts.Move == MoveFolder {
			dest = item.MoveTo
			if dest == "" {
				dest = opts.Folder
			}
			if dest == "" {
				return nil, errors.New("move to folder needs a folder for every email")
			}
		}
		if _, seen := byFolder[ref.folder]; !seen {
			folders = append(folders, ref.folder)
		}
		byFolder[ref.folder] = append(byFolder[ref.folder], pendingItem{id: item.ID, ref: ref, dest: dest})
	}

	if opts.Move != "" && !opts.DryRun && !c.imap.Caps().Has(imap.CapMove) && !c.imap.Caps().Has(imap.CapUIDPlus) {
		return nil, ErrUnsafeMove
	}

	result := &OrganizeResult{Missing: []string{}}
	for _, folder := range folders {
		present, err := c.presentItems(folder, byFolder[folder], opts.DryRun, result)
		if err != nil {
			return result, err
		}
		if len(present) == 0 || opts.DryRun {
			result.Changed += len(present)
			continue
		}
		if err := c.mark(present, opts.Mark); err != nil {
			return result, err
		}
		if err := c.move(folder, present, opts.Move); err != nil {
			return result, err
		}
		result.Changed += len(present)
	}
	return result, nil
}

// presentItems opens folder and keeps the items whose email is still there,
// recording the rest as missing.
func (c *Client) presentItems(folder string, items []pendingItem, readOnly bool, result *OrganizeResult) ([]pendingItem, error) {
	missing := func(items []pendingItem) {
		for _, item := range items {
			result.Missing = append(result.Missing, item.id)
		}
	}
	selected, err := c.imap.Select(folder, &imap.SelectOptions{ReadOnly: readOnly}).Wait()
	if err != nil {
		var imapErr *imap.Error
		if errors.As(err, &imapErr) && imapErr.Type == imap.StatusResponseTypeNo {
			missing(items)
			return nil, nil
		}
		return nil, fmt.Errorf("open folder %q: %w", folder, err)
	}

	var current []pendingItem
	var uids []imap.UID
	for _, item := range items {
		if item.ref.uidValidity != selected.UIDValidity {
			missing([]pendingItem{item})
			continue
		}
		current = append(current, item)
		uids = append(uids, item.ref.uid)
	}
	if len(uids) == 0 {
		return nil, nil
	}
	buffers, err := c.imap.Fetch(imap.UIDSetNum(uids...), &imap.FetchOptions{UID: true}).Collect()
	if err != nil {
		return nil, fmt.Errorf("check emails in %q: %w", folder, err)
	}
	exists := map[imap.UID]bool{}
	for _, buf := range buffers {
		exists[buf.UID] = true
	}
	var present []pendingItem
	for _, item := range current {
		if exists[item.ref.uid] {
			present = append(present, item)
		} else {
			missing([]pendingItem{item})
		}
	}
	return present, nil
}

func (c *Client) mark(items []pendingItem, mark string) error {
	var store *imap.StoreFlags
	switch mark {
	case "":
		return nil
	case MarkRead:
		store = &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagSeen}}
	case MarkUnread:
		store = &imap.StoreFlags{Op: imap.StoreFlagsDel, Flags: []imap.Flag{imap.FlagSeen}}
	case MarkFlagged:
		store = &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagFlagged}}
	case MarkUnflagged:
		store = &imap.StoreFlags{Op: imap.StoreFlagsDel, Flags: []imap.Flag{imap.FlagFlagged}}
	default:
		return fmt.Errorf("unknown mark %q", mark)
	}
	store.Silent = true
	if err := c.imap.Store(uidSet(items), store, nil).Close(); err != nil {
		return fmt.Errorf("mark emails %s: %w", mark, err)
	}
	return nil
}

func (c *Client) move(folder string, items []pendingItem, move string) error {
	switch move {
	case "":
		return nil
	case MoveArchive, MoveTrash:
		dest, err := c.specialUseFolder(move)
		if err != nil {
			return err
		}
		for i := range items {
			items[i].dest = dest
		}
	case MoveFolder:
	default:
		return fmt.Errorf("unknown move %q", move)
	}

	var dests []string
	byDest := map[string][]pendingItem{}
	for _, item := range items {
		if item.dest == folder {
			continue
		}
		if _, seen := byDest[item.dest]; !seen {
			dests = append(dests, item.dest)
		}
		byDest[item.dest] = append(byDest[item.dest], item)
	}
	for _, dest := range dests {
		err := c.moveTo(byDest[dest], dest)
		var imapErr *imap.Error
		if move == MoveFolder && errors.As(err, &imapErr) && imapErr.Code == imap.ResponseCodeTryCreate {
			if err := c.imap.Create(dest, nil).Wait(); err != nil {
				return fmt.Errorf("create folder %q: %w", dest, err)
			}
			err = c.moveTo(byDest[dest], dest)
		}
		if err != nil {
			return fmt.Errorf("move emails to %q: %w", dest, err)
		}
	}
	return nil
}

func (c *Client) moveTo(items []pendingItem, dest string) error {
	_, err := c.imap.Move(uidSet(items), dest).Wait()
	return err
}

// specialUseFolder finds the account's archive or trash folder from the
// special-use attributes in its folder list.
func (c *Client) specialUseFolder(move string) (string, error) {
	if c.specialUse == nil {
		folders, err := c.imap.List("", "*", nil).Collect()
		if err != nil {
			return "", fmt.Errorf("list folders: %w", err)
		}
		c.specialUse = map[imap.MailboxAttr]string{}
		for _, folder := range folders {
			for _, attr := range folder.Attrs {
				if _, seen := c.specialUse[attr]; !seen {
					c.specialUse[attr] = folder.Mailbox
				}
			}
		}
	}
	candidates := []imap.MailboxAttr{imap.MailboxAttrTrash}
	if move == MoveArchive {
		candidates = []imap.MailboxAttr{imap.MailboxAttrArchive, imap.MailboxAttrAll}
	}
	for _, attr := range candidates {
		if dest, ok := c.specialUse[attr]; ok {
			return dest, nil
		}
	}
	return "", fmt.Errorf("no folder is marked %s", joinAttrs(candidates))
}

func joinAttrs(attrs []imap.MailboxAttr) string {
	names := make([]string, len(attrs))
	for i, attr := range attrs {
		names[i] = string(attr)
	}
	return strings.Join(names, " or ")
}

func uidSet(items []pendingItem) imap.UIDSet {
	var set imap.UIDSet
	for _, item := range items {
		set.AddNum(item.ref.uid)
	}
	return set
}
