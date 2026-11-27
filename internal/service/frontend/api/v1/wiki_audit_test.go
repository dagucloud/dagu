// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"testing"

	apigen "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectregistry"
	apiv1 "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	"github.com/dagucloud/dagu/v2/internal/service/projectaccess"
	"github.com/dagucloud/dagu/v2/internal/wiki"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWikiAuditRecordsProject proves that a wiki change records the project
// whose wiki it changed, and records none while projects are off.
func TestWikiAuditRecordsProject(t *testing.T) {
	t.Parallel()

	registry := projectregistry.New(t.TempDir())
	defaultProject, err := registry.EnsureDefault(t.Context())
	require.NoError(t, err)

	for _, tc := range []struct {
		name        string
		projectsOn  bool
		wantID      string
		wantProject string
	}{
		{name: "projects on", projectsOn: true, wantID: defaultProject.ID, wantProject: "default"},
		{name: "projects off"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			auditStore := &recordingAuditStore{}
			options := []apiv1.APIOption{apiv1.WithAuditService(audit.New(auditStore))}
			if tc.projectsOn {
				options = append(options, apiv1.WithProjectAccess(projectaccess.New(registry)))
			}
			setup := newWikiPageTestSetupWithStoreOptions(t, &mockWikiStore{pages: map[string]*wiki.Page{}}, nil, options...)

			_, err := setup.api.CreateWikiPage(adminCtx(), apigen.CreateWikiPageRequestObject{
				Body: &apigen.CreateWikiPageJSONRequestBody{Id: "a", Content: "x"},
			})
			require.NoError(t, err)
			_, err = setup.api.DeleteWikiPageBatch(adminCtx(), apigen.DeleteWikiPageBatchRequestObject{
				Body: &apigen.DeleteWikiPageBatchJSONRequestBody{Paths: []string{"a"}},
			})
			require.NoError(t, err)

			require.Len(t, auditStore.entries, 2)
			for _, entry := range auditStore.entries {
				assert.Equal(t, tc.wantID, entry.ProjectID, entry.Action)
				assert.Equal(t, tc.wantProject, entry.Project, entry.Action)
			}
		})
	}
}
