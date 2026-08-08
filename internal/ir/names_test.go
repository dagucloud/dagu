// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"testing"
)

func TestValidateDAGName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr error
	}{
		{
			name:    "empty name is allowed",
			input:   "",
			wantErr: nil,
		},
		{
			name:    "valid name with alphanumeric characters",
			input:   "my-dag_123.0",
			wantErr: nil,
		},
		{
			name:    "name with spaces is invalid",
			input:   "my dag",
			wantErr: ErrNameInvalidChars,
		},
		{
			name:    "name with special characters is invalid",
			input:   "my!dag",
			wantErr: ErrNameInvalidChars,
		},
		{
			name:    "name that is too long",
			input:   "this-is-a-very-very-long-dag-name-that-is-way-too-long",
			wantErr: ErrNameTooLong,
		},
		{
			name:    "name at maximum allowed length",
			input:   "name-of-the-dag-exactly-forty-char", // 40 chars
			wantErr: nil,
		},
		{
			name:    "name just over maximum allowed length",
			input:   "nameofthedagexactlyfortycharactersandmore", // 41 chars
			wantErr: ErrNameTooLong,
		},

		// Test cases for Unicode names, which should fail with the current regex.
		{
			name:    "japanese name is invalid",
			input:   "私の-ダグ", // "my-dag" in Japanese
			wantErr: ErrNameInvalidChars,
		},
		{
			name:    "chinese name is invalid",
			input:   "我的-工作流", // "my-workflow" in Chinese
			wantErr: ErrNameInvalidChars,
		},
		{
			name:    "persian name is invalid",
			input:   "گرداب-من", // "my-whirlpool" in Persian
			wantErr: ErrNameInvalidChars,
		},
		{
			name:    "arabic name is invalid",
			input:   "سير-العمل-الخاص-بي", // "my-workflow" in Arabic
			wantErr: ErrNameInvalidChars,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDAGName(tc.input)

			if err != tc.wantErr {
				t.Errorf("ValidateDAGName(%q) got error %v, want %v", tc.input, err, tc.wantErr)
			}
		})
	}
}
