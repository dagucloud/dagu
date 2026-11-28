// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
)

// textError is an error whose message is its text, as the worker's
// failures, which start with a capital, are.
type textError string

func (e textError) Error() string { return string(e) }

// errRemovedForTest is a removed worker's failure, as the worker returns it.
var errRemovedForTest error = textError("This worker was removed from the coordinator\n  Get a new command")

// A removal the worker keeps a record of fails the check whatever the
// coordinators answered, as one that has not reread the revoked set yet still
// accepts the certificate (Spec 108 §dagu worker --check).
func TestWorkerCheckVerdict(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	assert.Equal(t, errRemovedForTest, workerCheckVerdict(&out, errRemovedForTest, 0, 1, false))
	assert.Equal(t, "This worker keeps a record that a coordinator refused its certificate as revoked, so it exits at start\n", out.String())

	out.Reset()
	assert.NoError(t, workerCheckVerdict(&out, nil, 0, 1, false))
	assert.Equal(t, "Every coordinator passed the check\n", out.String())
	assert.EqualError(t, workerCheckVerdict(&out, nil, 1, 2, false), "1 of 2 coordinators did not pass the check")
}

// An invite worker's check says whether the coordinator's CA matches the
// invite's pin, with the Spec 108 message for a coordinator of another Dagu,
// and prints the credential and the answer as the check gives them (Spec 108
// §dagu worker --check).
func TestWorkerCheckInviteLines(t *testing.T) {
	t.Parallel()

	handshake := &coordinator.TLSHandshake{Verified: true, Certificate: &coordinator.ServerCertificate{
		DNSNames: []string{"coordinator.dagu.invalid"}, CA: "Dagu peer CA",
	}}
	otherDagu := &coordinator.TLSHandshake{Err: coordinator.ErrInviteCAMismatch}
	removed := textError("This worker was removed from the coordinator\n  Get a new command from \"Add worker\" to add it again.")
	for _, tc := range []struct {
		name   string
		result coordinator.CheckResult
		invite bool
		want   string
	}{
		{
			name:   "PinMatches",
			result: coordinator.CheckResult{TLS: handshake},
			invite: true,
			want: "    Certificate chains to: Dagu peer CA\n" +
				"  Invite pin: matches the coordinator's CA\n" +
				"  Credential: no peer token\n" +
				"  Coordinator answered: serving, and it accepted the call\n",
		},
		{
			name:   "OtherDaguWithInvite",
			result: coordinator.CheckResult{TLS: otherDagu},
			invite: true,
			want: "  Handshake: failed, coordinator.example.com:50055 belongs to a different Dagu (its CA does not match the invite)\n" +
				"  Check the coordinator address.\n",
		},
		{
			name:   "OtherDaguWithoutInvite",
			result: coordinator.CheckResult{TLS: otherDagu},
			want: "  Handshake: failed, coordinator.example.com:50055 belongs to a different Dagu than the one this worker joined\n" +
				"  Get a new command from \"Add worker\" to join it.\n",
		},
		{
			name:   "Removed",
			result: coordinator.CheckResult{TLS: handshake, AnswerErr: removed},
			invite: true,
			want: "  Coordinator answered: This worker was removed from the coordinator\n" +
				"  Get a new command from \"Add worker\" to add it again.\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.result.Address = "coordinator.example.com:50055"
			tc.result.Transport = "built-in TLS (from worker certificate)"
			var out bytes.Buffer
			printWorkerCheck(&out, tc.result, tc.invite)
			assert.Contains(t, out.String(), tc.want)
		})
	}
}
