// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"html/template"
	"net/http"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/connection/connect"
)

// closePopupScript closes the consent popup that the UI opened. A tab that
// the account's owner opened stays, showing the message.
const closePopupScript = "window.close();"

// oauthCallbackCSP allows only closePopupScript to run.
var oauthCallbackCSP = func() string {
	sum := sha256.Sum256([]byte(closePopupScript))
	return "default-src 'none'; script-src 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}()

// oauthCallbackPage shows only a fixed message, never the code, the state,
// or an error text from the request.
var oauthCallbackPage = template.Must(template.New("oauth-callback").Parse(`<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>Dagu</title></head>
<body>
<p>{{.}}</p>
<script>` + closePopupScript + `</script>
</body>
</html>
`))

const (
	callbackDoneMessage    = "Sign-in complete. Return to Dagu to finish connecting. You can close this window."
	callbackRefusedMessage = "This sign-in link has expired or was already used. Start the connection again in Dagu."
	callbackFailedMessage  = "The sign-in did not complete. Return to Dagu for details."
)

// oauthCallbackHandler completes a connect session from a provider's
// redirect after consent.
func oauthCallbackHandler(engine *connect.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		err := engine.Callback(r.Context(), connect.CallbackParams{
			State:            q.Get("state"),
			Code:             q.Get("code"),
			Error:            q.Get("error"),
			ErrorDescription: q.Get("error_description"),
		})
		status, message := http.StatusOK, callbackDoneMessage
		switch {
		case errors.Is(err, connect.ErrCallbackRefused):
			status, message = http.StatusBadRequest, callbackRefusedMessage
		case errors.Is(err, connect.ErrConsentFailed):
			status, message = http.StatusBadRequest, callbackFailedMessage
		case err != nil:
			status, message = http.StatusInternalServerError, callbackFailedMessage
		}
		if err != nil {
			logger.Warn(r.Context(), "OAuth callback did not complete a connect session", tag.Error(err))
		}
		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Cache-Control", "no-store")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", oauthCallbackCSP)
		w.WriteHeader(status)
		_ = oauthCallbackPage.Execute(w, message)
	}
}
