// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec108_peer_transport_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// The server creates, lists, and deletes invites, reports join readiness,
// and removes workers through its coordinator, which uses the CA it
// generates. Only the create response contains the invite (Spec 108 §REST
// API, §Secrets in Output).
func TestInviteAPI(t *testing.T) {
	dagu := harness.NewRunner(t)
	services := startStartAll(t, dagu, "DAGU_AUTH_MODE=none")
	defer services.process.Stop()

	code, body := services.call(t, http.MethodPost, "/api/v1/workers/invites", `{"address": "192.168.1.20:50055"}`)
	require.Equal(t, http.StatusCreated, code, body)
	var created struct {
		ID        string `json:"id"`
		Invite    string `json:"invite"`
		ExpiresAt string `json:"expiresAt"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &created))
	assert.True(t, strings.HasPrefix(created.Invite, "dagu1."), created.Invite)
	assert.NotEmpty(t, created.ExpiresAt, "a single-use invite expires by default")

	code, body = services.call(t, http.MethodGet, "/api/v1/workers/invites", "")
	require.Equal(t, http.StatusOK, code, body)
	assert.Contains(t, body, `"id":"`+created.ID+`"`)
	assert.Contains(t, body, `"kind":"single_use"`)
	assert.NotContains(t, body, created.Invite)

	code, body = services.call(t, http.MethodGet, "/api/v1/workers/join-readiness", "")
	require.Equal(t, http.StatusOK, code, body)
	var readiness struct {
		Coordinators []struct {
			Report *struct {
				LoopbackOnly bool `json:"loopbackOnly"`
				BuiltinTLS   bool `json:"builtinTLS"`
				Ready        bool `json:"ready"`
			} `json:"report"`
		} `json:"coordinators"`
		LastInviteAddress string `json:"lastInviteAddress"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &readiness))
	require.Len(t, readiness.Coordinators, 1)
	report := readiness.Coordinators[0].Report
	require.NotNil(t, report, body)
	assert.True(t, report.BuiltinTLS)
	assert.True(t, report.LoopbackOnly, "the coordinator listens on 127.0.0.1")
	assert.False(t, report.Ready)
	assert.Equal(t, "192.168.1.20:50055", readiness.LastInviteAddress)
	assert.NotContains(t, body, created.Invite)

	code, body = services.call(t, http.MethodPost, "/api/v1/workers/invites", `{"expiresIn": "soon"}`)
	assert.Equal(t, http.StatusBadRequest, code, body)
	assert.Contains(t, body, "expiresIn")

	code, body = services.call(t, http.MethodDelete, "/api/v1/workers/invites/"+created.ID, "")
	assert.Equal(t, http.StatusNoContent, code, body)
	code, body = services.call(t, http.MethodDelete, "/api/v1/workers/invites/"+created.ID, "")
	assert.Equal(t, http.StatusNotFound, code, body)
	assert.NotContains(t, services.process.Output(), created.Invite)

	code, body = services.call(t, http.MethodDelete, "/api/v1/workers/unknown/certificate?certificateId=unknown&runs=stop", "")
	assert.Equal(t, http.StatusNotFound, code, body)
	code, body = services.call(t, http.MethodDelete, "/api/v1/workers/unknown/certificate?certificateId=unknown&runs=later", "")
	assert.Equal(t, http.StatusBadRequest, code, body)
}

// call sends method to path on the server of s with the JSON body, and
// returns the status and the response body.
func (s startAll) call(t *testing.T, method, path, body string) (int, string) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	req, err := http.NewRequestWithContext(callContext(t), method, "http://"+s.server+path, reader)
	require.NoError(t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(data)
}

// enrollWorker enrolls a new key as the worker id at the coordinator addr
// with invite, and returns a client on its certificate and the
// certificate's ID.
func (f peerFixture) enrollWorker(t *testing.T, addr, invite, id string) (coordinatorv1.CoordinatorServiceClient, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	require.NoError(t, err)
	admin := coordinatorv1.NewCoordinatorServiceClient(dialCoordinator(t, addr, f.builtinTLS()))
	enrolled, err := admin.Enroll(callContext(t), &coordinatorv1.EnrollRequest{
		Invite: invite, Csr: csr, WorkerId: id, Host: "host-" + id, KeepsCertificate: true,
	}, grpc.WaitForReady(true))
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(enrolled.Certificate)
	require.NoError(t, err)
	worker := coordinatorv1.NewCoordinatorServiceClient(dialCoordinator(t, addr, credentials.NewTLS(&tls.Config{
		RootCAs:      f.roots,
		ServerName:   coordinatorCertName,
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{{Certificate: [][]byte{enrolled.Certificate}, PrivateKey: key}},
	})))
	return worker, cert.Subject.CommonName
}

// The server lists workers that joined with an invite with their
// certificate and invite, and removes them through its coordinator: a
// connected worker becomes removing, with finish or stop, and one that is
// not connected is removed at once. A removal that names another
// certificate is refused (Spec 108 §REST API, §Removal).
func TestRemoveWorkerAPI(t *testing.T) {
	dagu := harness.NewRunner(t)
	peer := newPeerFixture(t, dagu)
	services := startStartAll(t, dagu, append(peer.env, "DAGU_AUTH_MODE=none")...)
	defer services.process.Stop()

	code, body := services.call(t, http.MethodPost, "/api/v1/workers/invites", `{"reusable": true}`)
	require.Equal(t, http.StatusCreated, code, body)
	var created struct {
		ID     string `json:"id"`
		Invite string `json:"invite"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &created))

	certs := map[string]string{}
	workers := map[string]coordinatorv1.CoordinatorServiceClient{}
	for _, id := range []string{"finish", "stop", "offline"} {
		workers[id], certs[id] = peer.enrollWorker(t, services.coordinator, created.Invite, id)
	}
	// Each connected worker holds a run of its own.
	held := map[string]*coordinatorv1.RunningTask{}
	for _, id := range []string{"finish", "stop"} {
		held[id] = services.holdRun(t, dagu, workers[id], id)
	}
	heartbeat := func(id string) *coordinatorv1.HeartbeatResponse {
		resp, err := workers[id].Heartbeat(callContext(t), &coordinatorv1.HeartbeatRequest{
			WorkerId: id, Host: "host-" + id,
			Stats: &coordinatorv1.WorkerStats{TotalPollers: 1, BusyPollers: 1, RunningTasks: []*coordinatorv1.RunningTask{held[id]}},
		})
		require.NoError(t, err)
		return resp
	}
	for _, id := range []string{"finish", "stop"} {
		assert.Empty(t, heartbeat(id).CancelledRuns, id)
	}
	for _, id := range []string{"finish", "stop"} {
		listed := services.waitForWorker(t, id)
		assert.Equal(t, "certificate", listed.Connection, id)
		assert.Equal(t, certs[id], listed.CertificateID, id)
		assert.Equal(t, created.ID, listed.InviteID, id)
		assert.False(t, listed.Removing, id)
	}

	remove := func(id, certID, runs string) (int, string) {
		return services.call(t, http.MethodDelete, "/api/v1/workers/"+id+"/certificate?certificateId="+certID+"&runs="+runs, "")
	}
	code, body = remove("finish", certs["stop"], "finish")
	assert.Equal(t, http.StatusConflict, code, body)
	for _, tt := range []struct{ id, runs, state string }{
		{"finish", "finish", "removing"},
		{"stop", "stop", "removing"},
		{"offline", "finish", "removed"},
	} {
		code, body = remove(tt.id, certs[tt.id], tt.runs)
		require.Equal(t, http.StatusOK, code, body)
		assert.JSONEq(t, `{"state": "`+tt.state+`"}`, body, tt.id)
	}
	for _, id := range []string{"finish", "stop"} {
		assert.True(t, services.waitForWorker(t, id).Removing, id)
	}
	// A stop that pairs the finish worker's certificate with another worker
	// changes nothing.
	code, body = remove("stop", certs["finish"], "stop")
	assert.Equal(t, http.StatusConflict, code, body)
	// Only stop asks the worker to cancel the run it holds.
	assert.Empty(t, heartbeat("finish").CancelledRuns)
	cancelled := heartbeat("stop").CancelledRuns
	require.Len(t, cancelled, 1)
	assert.Equal(t, held["stop"].AttemptKey, cancelled[0].AttemptKey)
	_, err := workers["offline"].GetWorkers(callContext(t), &coordinatorv1.GetWorkersRequest{})
	assert.Equal(t, codes.Unauthenticated, status.Code(err), "%v", err)

	code, body = remove("offline", certs["offline"], "finish")
	assert.Equal(t, http.StatusOK, code, body)
	assert.JSONEq(t, `{"state": "removed"}`, body, "a retried removal answers removed")
}

// holdRun enqueues a run of a DAG that only the worker id takes, has the
// worker poll and accept it as worker, and returns the running task its
// heartbeats report.
func (s startAll) holdRun(t *testing.T, dagu *harness.Runner, worker coordinatorv1.CoordinatorServiceClient, id string) *coordinatorv1.RunningTask {
	t.Helper()
	dag := "held_" + id + ".yaml"
	dagu.WriteFile(dag, "worker_selector:\n  holder: "+id+"\nsteps:\n  - id: wait\n    run: sleep 60\n")
	ctx := callContext(t)
	labels := map[string]string{"holder": id}
	// The queue dispatches only to a worker whose heartbeat matches.
	_, err := worker.Heartbeat(ctx, &coordinatorv1.HeartbeatRequest{
		WorkerId: id, Host: "host-" + id, Labels: labels, Stats: &coordinatorv1.WorkerStats{TotalPollers: 1},
	})
	require.NoError(t, err)
	dagu.RunWithEnv([]string{"DAGU_HOME=" + s.home}, "enqueue", "--run-id=held-"+id, dag).ExpectExitCode(0)

	var task *coordinatorv1.Task
	for task == nil {
		resp, err := worker.Poll(ctx, &coordinatorv1.PollRequest{WorkerId: id, PollerId: id + "-poller", Labels: labels})
		require.NoError(t, err)
		task = resp.Task
	}
	ack, err := worker.AckTaskClaim(ctx, &coordinatorv1.AckTaskClaimRequest{WorkerId: id, ClaimToken: task.ClaimToken, AttemptKey: task.AttemptKey})
	require.NoError(t, err)
	require.True(t, ack.Accepted, id)
	return &coordinatorv1.RunningTask{DagName: "held_" + id, DagRunId: task.DagRunId, AttemptKey: task.AttemptKey}
}
