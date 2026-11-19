// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"strconv"
	"text/tabwriter"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
)

// printUntrusted prints the restricted user of the block untrusted (Spec 092):
// the value that applies for each key, and where the value came from. The
// block has no secret.
func printUntrusted(ctx *Context) error {
	untrusted := ctx.Config.Untrusted
	values := map[string]string{}
	sources := map[string]string{}
	if untrusted.Enabled {
		values["uid"] = strconv.FormatUint(uint64(untrusted.UID), 10)
		values["gid"] = strconv.FormatUint(uint64(untrusted.GID), 10)
		sources["uid"], sources["gid"] = "file", "file"
	}
	if untrusted.IsolationOff {
		values["isolation"] = config.UntrustedIsolationOff
		sources["isolation"] = "file"
	}
	out := ctx.Command.OutOrStdout()
	_, _ = fmt.Fprintln(out, "\nUntrusted (key, value that applies, source):")
	w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	for _, doc := range config.UntrustedKeyDocs {
		value, ok := values[doc.Key]
		source := sources[doc.Key]
		if !ok {
			value = "(" + doc.Default + ")"
			source = "not set"
		}
		_, _ = fmt.Fprintf(w, "  untrusted.%s:\t%s\t%s\n", doc.Key, value, source)
	}
	return w.Flush()
}
