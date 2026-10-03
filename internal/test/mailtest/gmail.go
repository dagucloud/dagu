// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mailtest

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/textproto"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/api/gmail/v1"
)

// Gmail is an in-process Gmail API serving one mailbox. It answers the calls
// the mailbox package makes and fails the test on any other request.
type Gmail struct {
	// URL is the API endpoint.
	URL string
	// Token is the access token every request must carry.
	Token string

	t           testing.TB
	mu          sync.Mutex
	labels      []*gmail.Label
	messages    []*gmailMessage
	sent        []GmailSent
	appended    int
	scopeDenied bool
}

// GmailSent is a message the API sent.
type GmailSent struct {
	Raw      string
	ThreadID string
}

type gmailMessage struct {
	id       string
	raw      []byte
	labels   []string
	received time.Time
}

var gmailSystemLabels = []string{"INBOX", "SENT", "DRAFT", "SPAM", "TRASH", "UNREAD", "STARRED", "IMPORTANT"}

// StartGmail starts an API with an empty mailbox. It stops when the test ends.
func StartGmail(t testing.TB) *Gmail {
	t.Helper()
	g := &Gmail{Token: "gmail-access-token", t: t}
	for _, id := range gmailSystemLabels {
		g.labels = append(g.labels, &gmail.Label{Id: id, Name: id, Type: "system"})
	}

	const base = "/gmail/v1/users/me/"
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+base+"labels", g.listLabels)
	mux.HandleFunc("POST "+base+"labels", g.createLabel)
	mux.HandleFunc("GET "+base+"messages", g.listMessages)
	mux.HandleFunc("GET "+base+"messages/{id}", g.getMessage)
	mux.HandleFunc("POST "+base+"messages/batchModify", g.batchModify)
	mux.HandleFunc("POST "+base+"messages/{id}/trash", g.trash)
	mux.HandleFunc("POST /upload"+base+"messages/send", g.send)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected Gmail API request %s %s", r.Method, r.URL.Path)
		writeGmailError(w, http.StatusNotFound, "unexpected request", "notFound")
	})
	server := httptest.NewServer(g.authorize(mux))
	t.Cleanup(server.Close)
	g.URL = server.URL + "/"
	return g
}

// CreateLabel creates a user label and returns its ID.
func (g *Gmail) CreateLabel(t testing.TB, name string) string {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.addLabel(name).Id
}

// Append stores a raw RFC 5322 message under the labels, by name, and returns
// its message ID. It counts as received now.
func (g *Gmail) Append(t testing.TB, raw string, labels ...string) string {
	t.Helper()
	return g.AppendAt(t, raw, time.Now(), labels...)
}

// AppendAt is Append with the time the mailbox received the message.
func (g *Gmail) AppendAt(t testing.TB, raw string, received time.Time, labels ...string) string {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	ids := make([]string, 0, len(labels))
	for _, name := range labels {
		label := g.labelNamed(name)
		if label == nil {
			t.Fatalf("no label %q", name)
		}
		ids = append(ids, label.Id)
	}
	g.appended++
	msg := &gmailMessage{
		id:       fmt.Sprintf("m%d", g.appended),
		raw:      []byte(raw),
		labels:   ids,
		received: received,
	}
	g.messages = append(g.messages, msg)
	return msg.id
}

// Labels returns the names of the labels on the email with id, or nil when it
// is gone.
func (g *Gmail) Labels(t testing.TB, id string) []string {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	msg := g.message(id)
	if msg == nil {
		return nil
	}
	names := []string{}
	for _, labelID := range msg.labels {
		names = append(names, g.labelWithID(labelID).Name)
	}
	slices.Sort(names)
	return names
}

// Subjects lists the subjects of the email under the label, by name, oldest
// first. As in Gmail, a label other than SPAM or TRASH leaves out email in
// either.
func (g *Gmail) Subjects(t testing.TB, labelName string) []string {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	label := g.labelNamed(labelName)
	if label == nil {
		t.Fatalf("no label %q", labelName)
	}
	hidden := !slices.Contains([]string{"SPAM", "TRASH"}, label.Id)
	subjects := []string{}
	for _, msg := range g.messages {
		if !slices.Contains(msg.labels, label.Id) || hidden && msg.inSpamOrTrash() {
			continue
		}
		parsed, err := mail.ReadMessage(bytes.NewReader(msg.raw))
		if err != nil {
			t.Fatalf("parse message %s: %v", msg.id, err)
		}
		subjects = append(subjects, parsed.Header.Get("Subject"))
	}
	return subjects
}

// Sent returns the messages sent so far.
func (g *Gmail) Sent() []GmailSent {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.sent)
}

// AddLabels puts labels, by name, on the email with id, as a person or a
// filter in Gmail does.
func (g *Gmail) AddLabels(t testing.TB, id string, labels ...string) {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	msg := g.message(id)
	if msg == nil {
		t.Fatalf("no email %q", id)
	}
	for _, name := range labels {
		label := g.labelNamed(name)
		if label == nil {
			t.Fatalf("no label %q", name)
		}
		if !slices.Contains(msg.labels, label.Id) {
			msg.labels = append(msg.labels, label.Id)
		}
	}
}

// Delete removes the email with id for good, as emptying the trash does.
func (g *Gmail) Delete(t testing.TB, id string) {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.messages = slices.DeleteFunc(g.messages, func(msg *gmailMessage) bool { return msg.id == id })
}

// DenyScope answers every later request as Gmail does for a token that does
// not grant Gmail access.
func (g *Gmail) DenyScope() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.scopeDenied = true
}

func (g *Gmail) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+g.Token {
			writeGmailError(w, http.StatusUnauthorized, "Request had invalid authentication credentials.", "authError")
			return
		}
		g.mu.Lock()
		denied := g.scopeDenied
		g.mu.Unlock()
		if denied {
			writeGmailError(w, http.StatusForbidden, "Request had insufficient authentication scopes.", "insufficientPermissions")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (g *Gmail) listLabels(w http.ResponseWriter, _ *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	writeGmailJSON(w, &gmail.ListLabelsResponse{Labels: g.labels})
}

func (g *Gmail) createLabel(w http.ResponseWriter, r *http.Request) {
	var request gmail.Label
	if !g.decode(w, r, &request) {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.labelNamed(request.Name) != nil {
		writeGmailError(w, http.StatusConflict, "Label name exists or conflicts", "duplicate")
		return
	}
	writeGmailJSON(w, g.addLabel(request.Name))
}

func (g *Gmail) listMessages(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	labelIDs := query["labelIds"]
	includeSpamTrash := query.Get("includeSpamTrash") == "true"
	after := int64(-1)
	hasAttachment := false
	for term := range strings.FieldsSeq(query.Get("q")) {
		switch {
		case strings.HasPrefix(term, "after:"):
			seconds, err := strconv.ParseInt(strings.TrimPrefix(term, "after:"), 10, 64)
			if err != nil {
				g.t.Errorf("Gmail query term %q is not epoch seconds", term)
			}
			after = seconds
		case term == "has:attachment":
			hasAttachment = true
		default:
			g.t.Errorf("unsupported Gmail query term %q", term)
			writeGmailError(w, http.StatusBadRequest, "unsupported query", "invalidArgument")
			return
		}
	}
	pageSize, err := strconv.Atoi(cmp.Or(query.Get("maxResults"), "100"))
	if err != nil {
		g.t.Errorf("maxResults %q: %v", query.Get("maxResults"), err)
	}
	offset, _ := strconv.Atoi(query.Get("pageToken"))

	g.mu.Lock()
	defer g.mu.Unlock()
	var matched []*gmailMessage
	// Newest first: by time received, then the later appended.
	for _, msg := range slices.Backward(g.messages) {
		switch {
		case !containsAll(msg.labels, labelIDs),
			!includeSpamTrash && msg.inSpamOrTrash(),
			after >= 0 && msg.received.Unix() <= after,
			hasAttachment && !bytes.Contains(bytes.ToLower(msg.raw), []byte("content-disposition: attachment")):
			continue
		}
		matched = append(matched, msg)
	}
	slices.SortStableFunc(matched, func(a, b *gmailMessage) int { return b.received.Compare(a.received) })

	response := &gmail.ListMessagesResponse{ResultSizeEstimate: int64(len(matched))}
	end := min(offset+pageSize, len(matched))
	for _, msg := range matched[min(offset, end):end] {
		response.Messages = append(response.Messages, &gmail.Message{Id: msg.id, ThreadId: threadID(msg.id)})
	}
	if end < len(matched) {
		response.NextPageToken = strconv.Itoa(end)
	}
	writeGmailJSON(w, response)
}

func (g *Gmail) getMessage(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	msg := g.message(r.PathValue("id"))
	if msg == nil {
		writeGmailError(w, http.StatusNotFound, "Requested entity was not found.", "notFound")
		return
	}
	response := msg.resource()
	switch format := r.URL.Query().Get("format"); format {
	case "minimal":
	case "metadata":
		header, err := textproto.NewReader(bufio.NewReader(bytes.NewReader(msg.raw))).ReadMIMEHeader()
		if err != nil && len(header) == 0 {
			g.t.Errorf("parse headers of %s: %v", msg.id, err)
		}
		response.Payload = &gmail.MessagePart{}
		for _, name := range r.URL.Query()["metadataHeaders"] {
			for _, value := range header.Values(name) {
				response.Payload.Headers = append(response.Payload.Headers, &gmail.MessagePartHeader{Name: name, Value: value})
			}
		}
	case "raw":
		response.Raw = base64.URLEncoding.EncodeToString(msg.raw)
	default:
		g.t.Errorf("unsupported message format %q", format)
	}
	writeGmailJSON(w, response)
}

func (g *Gmail) batchModify(w http.ResponseWriter, r *http.Request) {
	var request gmail.BatchModifyMessagesRequest
	if !g.decode(w, r, &request) {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, labelID := range slices.Concat(request.AddLabelIds, request.RemoveLabelIds) {
		// Gmail moves email to the trash only through its own call.
		if g.labelWithID(labelID) == nil || labelID == "TRASH" && slices.Contains(request.AddLabelIds, labelID) {
			writeGmailError(w, http.StatusBadRequest, "Invalid label: "+labelID, "invalidArgument")
			return
		}
	}
	for _, id := range request.Ids {
		msg := g.message(id)
		if msg == nil {
			writeGmailError(w, http.StatusBadRequest, "Invalid id value", "invalidArgument")
			return
		}
		msg.labels = slices.DeleteFunc(msg.labels, func(label string) bool {
			return slices.Contains(request.RemoveLabelIds, label)
		})
		for _, label := range request.AddLabelIds {
			if !slices.Contains(msg.labels, label) {
				msg.labels = append(msg.labels, label)
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gmail) trash(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	msg := g.message(r.PathValue("id"))
	if msg == nil {
		writeGmailError(w, http.StatusNotFound, "Requested entity was not found.", "notFound")
		return
	}
	// Gmail keeps the other labels so the email can come back where it was.
	if !slices.Contains(msg.labels, "TRASH") {
		msg.labels = append(msg.labels, "TRASH")
	}
	writeGmailJSON(w, msg.resource())
}

func (g *Gmail) send(w http.ResponseWriter, r *http.Request) {
	if uploadType := r.URL.Query().Get("uploadType"); uploadType != "multipart" {
		g.t.Errorf("send uploadType = %q, want multipart", uploadType)
	}
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/related" {
		g.t.Errorf("send Content-Type = %q", r.Header.Get("Content-Type"))
		writeGmailError(w, http.StatusBadRequest, "expected multipart/related", "invalidArgument")
		return
	}
	reader := multipart.NewReader(r.Body, params["boundary"])
	var metadata gmail.Message
	var raw []byte
	for i := 0; ; i++ {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			g.t.Errorf("read send part: %v", err)
			return
		}
		data, err := io.ReadAll(part)
		if err != nil {
			g.t.Errorf("read send part: %v", err)
			return
		}
		if i == 0 {
			if err := json.Unmarshal(data, &metadata); err != nil {
				g.t.Errorf("decode send metadata: %v", err)
			}
		} else {
			raw = data
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sent = append(g.sent, GmailSent{Raw: string(raw), ThreadID: metadata.ThreadId})
	id := fmt.Sprintf("s%d", len(g.sent))
	writeGmailJSON(w, &gmail.Message{Id: id, ThreadId: cmp.Or(metadata.ThreadId, threadID(id)), LabelIds: []string{"SENT"}})
}

func (g *Gmail) decode(w http.ResponseWriter, r *http.Request, into any) bool {
	if err := json.NewDecoder(r.Body).Decode(into); err != nil {
		g.t.Errorf("decode %s %s: %v", r.Method, r.URL.Path, err)
		writeGmailError(w, http.StatusBadRequest, err.Error(), "invalidArgument")
		return false
	}
	return true
}

func (g *Gmail) addLabel(name string) *gmail.Label {
	label := &gmail.Label{Id: fmt.Sprintf("Label_%d", len(g.labels)+1), Name: name, Type: "user"}
	g.labels = append(g.labels, label)
	return label
}

func (g *Gmail) labelNamed(name string) *gmail.Label {
	for _, label := range g.labels {
		if label.Name == name {
			return label
		}
	}
	return nil
}

func (g *Gmail) labelWithID(id string) *gmail.Label {
	for _, label := range g.labels {
		if label.Id == id {
			return label
		}
	}
	return nil
}

func (g *Gmail) message(id string) *gmailMessage {
	for _, msg := range g.messages {
		if msg.id == id {
			return msg
		}
	}
	return nil
}

func (m *gmailMessage) resource() *gmail.Message {
	return &gmail.Message{
		Id:           m.id,
		ThreadId:     threadID(m.id),
		LabelIds:     slices.Clone(m.labels),
		InternalDate: m.received.UnixMilli(),
		SizeEstimate: int64(len(m.raw)),
	}
}

func (m *gmailMessage) inSpamOrTrash() bool {
	return slices.Contains(m.labels, "SPAM") || slices.Contains(m.labels, "TRASH")
}

// threadID gives every appended email a conversation of its own.
func threadID(messageID string) string {
	return "t-" + messageID
}

func containsAll(have, want []string) bool {
	for _, item := range want {
		if !slices.Contains(have, item) {
			return false
		}
	}
	return true
}

func writeGmailJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

// writeGmailError answers in the error format Google APIs use.
func writeGmailError(w http.ResponseWriter, code int, message, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
			"errors":  []map[string]string{{"message": message, "domain": "global", "reason": reason}},
		},
	})
}
