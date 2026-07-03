// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package docker_test

import (
	"testing"

	dockerruntime "github.com/dagucloud/dagu/internal/runtime/builtin/docker"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
)

func TestImagePlatformMatchesForTest(t *testing.T) {
	tests := []struct {
		name   string
		target specs.Platform
		image  specs.Platform
		want   bool
	}{
		{
			name:   "ExactMatch",
			target: specs.Platform{OS: "linux", Architecture: "amd64"},
			image:  specs.Platform{OS: "linux", Architecture: "amd64"},
			want:   true,
		},
		{
			name:   "Arm64DefaultMatchesV8Image",
			target: specs.Platform{OS: "linux", Architecture: "arm64"},
			image:  specs.Platform{OS: "linux", Architecture: "arm64", Variant: "v8"},
			want:   true,
		},
		{
			name:   "DifferentArchitectureDoesNotMatch",
			target: specs.Platform{OS: "linux", Architecture: "amd64"},
			image:  specs.Platform{OS: "linux", Architecture: "arm64", Variant: "v8"},
			want:   false,
		},
		{
			name:   "Arm64DefaultDoesNotMatchArmImage",
			target: specs.Platform{OS: "linux", Architecture: "arm64"},
			image:  specs.Platform{OS: "linux", Architecture: "arm", Variant: "v8"},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dockerruntime.ImagePlatformMatchesForTest(tt.target, tt.image)
			assert.Equal(t, tt.want, got)
		})
	}
}
