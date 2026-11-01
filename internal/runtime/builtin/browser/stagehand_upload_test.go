// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// receiptsPage has a labeled file input; a drop area holding a hidden one;
// a button that opens a hidden one elsewhere on the page; and a drop area
// that opens nothing when clicked. Each file input logs the files it gets,
// and so do frames, by message.
const receiptsPage = `<!doctype html><html><head><title>Receipts</title></head><body>
<label>Receipt <input id="visible" type="file"></label>
<div id="zone">Drop invoices here<input id="zone-input" type="file" multiple hidden></div>
<input id="picked" type="file" multiple hidden>
<button onclick="document.getElementById('picked').click()">Choose files</button>
<div id="quiet"><p>Drop statements here</p><input id="quiet-input" type="file" hidden></div>
<pre id="log"></pre>
<script>
for (const input of document.querySelectorAll('input[type=file]')) {
  input.addEventListener('change', () => {
    document.getElementById('log').textContent += input.id + ' got ' + [...input.files].map(f => f.name + ':' + f.type + ':' + f.size).join(',') + '\n';
  });
}
addEventListener('message', (event) => { document.getElementById('log').textContent += event.data + '\n'; });
</script>
</body></html>`

// framedPage holds the page at %s in a frame.
const framedPage = `<!doctype html><html><head><title>Framed</title></head><body>
<iframe src="%s" width="600" height="300"></iframe>
<pre id="log"></pre>
<script>addEventListener('message', (event) => { document.getElementById('log').textContent += event.data + '\n'; });</script>
</body></html>`

// framePage is a page meant for a frame: a button that opens a hidden file
// input, which reports what it gets to the page around the frame.
const framePage = `<!doctype html><html><body>
<input id="framed-input" type="file" hidden onchange="parent.postMessage('framed-input got ' + [...this.files].map(f => f.name).join(','), '*')">
<button onclick="document.getElementById('framed-input').click()">Attach statement</button>
</body></html>`

// serveReceipts serves the receipts page, and a page framing it from
// another site, which the browser runs as a frame of its own.
func serveReceipts(t *testing.T) (receipts, framed string) {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/framed":
			_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
			_, _ = io.WriteString(w, strings.Replace(framedPage, "%s", "http://localhost:"+port+"/frame", 1))
		case "/frame":
			_, _ = io.WriteString(w, framePage)
		default:
			_, _ = io.WriteString(w, receiptsPage)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL + "/receipts", server.URL + "/framed"
}

// receiptFiles writes files to upload and returns them.
func receiptFiles(t *testing.T, names ...string) []uploadFile {
	t.Helper()
	dir := t.TempDir()
	files := make([]uploadFile, 0, len(names))
	for _, name := range names {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, []byte("data of "+name), 0o600))
		file, err := statUploadFile(path)
		require.NoError(t, err)
		files = append(files, file)
	}
	return files
}

// pageLog waits for the page's log to show want and returns it.
func pageLog(t *testing.T, eng engine, want string) string {
	t.Helper()
	var text string
	assert.Eventually(t, func() bool {
		text, _ = eng.PageText(t.Context())
		return strings.Contains(text, want)
	}, 10*time.Second, 100*time.Millisecond, "the page logs %q", want)
	return text
}

func TestStagehandUpload(t *testing.T) {
	t.Parallel()

	eng := launchBrowser(t, launchOptions{Generate: (&shopModel{}).generate})
	receipts, _ := serveReceipts(t)
	require.NoError(t, eng.Goto(t.Context(), receipts, time.Minute))
	invoice := receiptFiles(t, "invoice.pdf")
	both := receiptFiles(t, "a.pdf", "b.xlsx")

	for _, tc := range []struct {
		name     string
		selector string
		files    []uploadFile
		how      string
		log      string
	}{
		{
			name:     "a file input",
			selector: "xpath=/html/body/label[1]/input[1]",
			files:    invoice, how: uploadDirect,
			log: "visible got invoice.pdf:application/pdf:19",
		},
		{
			name:     "a drop area holding a file input",
			selector: "xpath=/html/body/div[1]",
			files:    both, how: uploadDirect,
			log: "zone-input got a.pdf:application/pdf:13,b.xlsx:application/vnd.openxmlformats-officedocument.spreadsheetml.sheet:14",
		},
		{
			name:     "a button that opens a file picker",
			selector: "xpath=/html/body/button[1]",
			files:    both, how: uploadPicker,
			log: "picked got a.pdf:application/pdf:13,b.xlsx:application/vnd.openxmlformats-officedocument.spreadsheetml.sheet:14",
		},
		{
			name:     "text in a drop area that opens nothing",
			selector: "xpath=/html/body/div[2]/p[1]",
			files:    invoice, how: uploadNearby,
			log: "quiet-input got invoice.pdf:application/pdf:19",
		},
	} {
		how, err := eng.Upload(t.Context(), tc.selector, tc.files, time.Minute)
		require.NoError(t, err, tc.name)
		assert.Equal(t, tc.how, how, tc.name)
		pageLog(t, eng, tc.log)
	}
}

func TestStagehandUploadRefuses(t *testing.T) {
	t.Parallel()

	eng := launchBrowser(t, launchOptions{Generate: (&shopModel{}).generate})
	receipts, _ := serveReceipts(t)
	require.NoError(t, eng.Goto(t.Context(), receipts, time.Minute))
	both := receiptFiles(t, "a.pdf", "b.pdf")

	_, err := eng.Upload(t.Context(), "", both, time.Minute)
	require.ErrorIs(t, err, errFileInputs{count: 4})

	_, err = eng.Upload(t.Context(), "xpath=/html/body/label[1]/input[1]", both, time.Minute)
	require.ErrorIs(t, err, errSingleFile{files: 2}, "a file input that takes one file")

	_, err = eng.Upload(t.Context(), "xpath=/html/body/div[2]/p[1]", both, time.Minute)
	require.ErrorIs(t, err, errSingleFile{files: 2}, "the file input nearest the text takes one file")

	_, err = eng.Upload(t.Context(), "xpath=/html/body/pre[1]", both, time.Minute)
	require.ErrorContains(t, err, "no single file input is near it", "the page around the log holds several")

	require.NoError(t, eng.Goto(t.Context(), `data:text/html,<h1>No inputs</h1>`, time.Minute))
	_, err = eng.Upload(t.Context(), "", both, time.Minute)
	require.ErrorIs(t, err, errNoFileInput)
	_, err = eng.Upload(t.Context(), "xpath=/html/body/h1[1]", both, time.Minute)
	require.ErrorContains(t, err, "clicking the element opened no file picker, and no single file input is near it")
}

// A frame from another site runs apart from the page, and its file picker
// is intercepted all the same.
func TestStagehandUploadInAFrame(t *testing.T) {
	t.Parallel()

	eng := launchBrowser(t, launchOptions{Generate: (&shopModel{}).generate})
	_, framed := serveReceipts(t)
	require.NoError(t, eng.Goto(t.Context(), framed, time.Minute))
	snap, err := eng.Snapshot(t.Context())
	require.NoError(t, err)
	selector := ""
	for id, xpath := range snap.XPaths {
		if strings.Contains(snap.Tree, "["+id+"] button: Attach statement") {
			selector = "xpath=" + xpath
		}
	}
	require.NotEmpty(t, selector, snap.Tree)

	how, err := eng.Upload(t.Context(), selector, receiptFiles(t, "statement.pdf"), time.Minute)
	require.NoError(t, err)
	assert.Equal(t, uploadPicker, how)
	pageLog(t, eng, "framed-input got statement.pdf")
}

// The outline shows a file input with its ID, so an upload can name it.
func TestStagehandOutlinesFileInputs(t *testing.T) {
	t.Parallel()

	eng := launchBrowser(t, launchOptions{Generate: (&shopModel{}).generate})
	receipts, _ := serveReceipts(t)
	require.NoError(t, eng.Goto(t.Context(), receipts, time.Minute))
	snap, err := eng.Snapshot(t.Context())
	require.NoError(t, err)
	outline, _, _ := renderOutline(snap, outlineOptions{})
	assert.Regexp(t, `\[\d+-\d+\] file input "Receipt"`, outline)
}

// An upload without into finds the page's only file input in the frames it
// shows, as a page built of frames holds its form in one.
func TestStagehandUploadFindsTheFileInputInAFrame(t *testing.T) {
	t.Parallel()

	pages := map[string]string{
		"/":     `<frameset cols="30%,70%"><frame src="/menu"><frame src="/form"></frameset>`,
		"/menu": `<p>Menu</p>`,
		"/form": `<input id="form-input" type="file" onchange="document.getElementById('log').textContent = 'form-input got ' + [...this.files].map(f => f.name).join(',')"><pre id="log"></pre>`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, pages[r.URL.Path])
	}))
	t.Cleanup(server.Close)
	eng := launchBrowser(t, launchOptions{Generate: (&shopModel{}).generate})
	require.NoError(t, eng.Goto(t.Context(), server.URL, time.Minute))

	how, err := eng.Upload(t.Context(), "", receiptFiles(t, "bid.pdf"), time.Minute)
	require.NoError(t, err)
	assert.Equal(t, uploadDirect, how)
	pageLog(t, eng, "form-input got bid.pdf")
}
