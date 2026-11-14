// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The service and its stop command reach each other only through the event
// name, so both must derive the same one from the data directory.
func TestServiceStopEventName(t *testing.T) {
	t.Parallel()

	name := serviceStopEventName(`C:\ProgramData\Dagu\data`)
	assert.Regexp(t, `^Local\\dagu-service-stop-[0-9a-f]{16}$`, name)
	assert.Equal(t, name, serviceStopEventName(`c:\programdata\dagu\data`), "Windows paths ignore case")
	assert.NotEqual(t, name, serviceStopEventName(`C:\ProgramData\Dagu\second`))
}
