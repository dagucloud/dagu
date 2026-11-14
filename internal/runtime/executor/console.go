// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"fmt"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/ianaindex"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
)

// encodingForCodePage returns the encoding of a Windows code page, or nil
// when the code page is UTF-8 or unknown.
func encodingForCodePage(codePage uint32) encoding.Encoding {
	switch codePage {
	case 932:
		return japanese.ShiftJIS
	case 936:
		return simplifiedchinese.GBK
	case 949:
		return korean.EUCKR
	case 950:
		return traditionalchinese.Big5
	}
	// ANSI code pages are registered as "windows-<n>", OEM ones as "ibm<n>".
	for _, name := range []string{fmt.Sprintf("windows-%d", codePage), fmt.Sprintf("ibm%d", codePage)} {
		if enc, err := ianaindex.IANA.Encoding(name); err == nil && enc != nil {
			return enc
		}
	}
	return nil
}
