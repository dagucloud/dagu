// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package profile

import (
	"encoding/hex"
	"fmt"
)

func SecretRef(profileName, key string) string {
	return fmt.Sprintf("runtime-profiles/%s/key-%s", profileName, hex.EncodeToString([]byte(key)))
}
