// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package anthropicbeta_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dagucloud/dagu/v2/internal/llm/anthropicbeta"
)

func TestJoin(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		values []string
		want   string
	}{
		"no value":     {nil, anthropicbeta.OAuth},
		"other betas":  {[]string{"a-1, b-2", "c-3"}, "a-1,b-2,c-3," + anthropicbeta.OAuth},
		"beta present": {[]string{"a-1," + anthropicbeta.OAuth}, "a-1," + anthropicbeta.OAuth},
		"empty items":  {[]string{" , a-1,,"}, "a-1," + anthropicbeta.OAuth},
	} {
		assert.Equal(t, tc.want, anthropicbeta.Join(tc.values, anthropicbeta.OAuth), name)
	}
}
