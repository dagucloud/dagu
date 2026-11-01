// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intake

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// WebhookRuntimeParams are the params a webhook's run receives: the request
// body as WEBHOOK_PAYLOAD and the forwarded headers, encoded by
// WebhookHeaders, as WEBHOOK_HEADERS.
func WebhookRuntimeParams(payload, headers string) string {
	return fmt.Sprintf("WEBHOOK_PAYLOAD=%s WEBHOOK_HEADERS=%s", strconv.Quote(payload), strconv.Quote(headers))
}

// WebhookHeaders encodes the request headers a webhook's run receives: those
// named in forward, under lowercase names, each with the values header gives
// for it. Names the run must never see, such as Authorization, are left out.
func WebhookHeaders(forward []string, header func(name string) []string) (string, error) {
	forwarded := make(map[string][]string)
	for _, raw := range forward {
		name := ir.NormalizeWebhookForwardHeader(raw)
		if name == "" || ir.IsDeniedWebhookForwardHeader(name) {
			continue
		}
		if values := header(name); len(values) > 0 {
			forwarded[name] = append([]string(nil), values...)
		}
	}
	encoded, err := json.Marshal(forwarded)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
