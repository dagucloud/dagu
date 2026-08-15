// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/opencodehost"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

const managedAgentOwnerStaleThreshold = 30 * time.Second

type agentSessionActionError struct {
	notFound bool
	conflict bool
	message  string
}

func (e *agentSessionActionError) Error() string { return e.message }

// RespondDAGRunStepAgentInteraction records an answer and resumes the managed session.
func (a *API) RespondDAGRunStepAgentInteraction(ctx context.Context, request api.RespondDAGRunStepAgentInteractionRequestObject) (api.RespondDAGRunStepAgentInteractionResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}
	response, err := a.respondAgentInteraction(ctx, ir.NewDAGRunRef(request.Name, request.DagRunId), "", request.StepName, request.InteractionId, request.Body)
	if err != nil {
		var actionErr *agentSessionActionError
		if errors.As(err, &actionErr) && actionErr.notFound {
			return &api.RespondDAGRunStepAgentInteraction404JSONResponse{Code: api.ErrorCodeNotFound, Message: err.Error()}, nil
		}
		if errors.As(err, &actionErr) && actionErr.conflict {
			return &api.RespondDAGRunStepAgentInteraction409JSONResponse{Code: api.ErrorCodeConflict, Message: err.Error()}, nil
		}
		return &api.RespondDAGRunStepAgentInteraction400JSONResponse{Code: api.ErrorCodeBadRequest, Message: err.Error()}, nil
	}
	return (*api.RespondDAGRunStepAgentInteraction200JSONResponse)(&response), nil
}

// RespondSubDAGRunStepAgentInteraction records an answer for a sub DAG-run session.
func (a *API) RespondSubDAGRunStepAgentInteraction(ctx context.Context, request api.RespondSubDAGRunStepAgentInteractionRequestObject) (api.RespondSubDAGRunStepAgentInteractionResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}
	response, err := a.respondAgentInteraction(ctx, ir.NewDAGRunRef(request.Name, request.DagRunId), request.SubDAGRunId, request.StepName, request.InteractionId, request.Body)
	if err != nil {
		var actionErr *agentSessionActionError
		if errors.As(err, &actionErr) && actionErr.notFound {
			return &api.RespondSubDAGRunStepAgentInteraction404JSONResponse{Code: api.ErrorCodeNotFound, Message: err.Error()}, nil
		}
		if errors.As(err, &actionErr) && actionErr.conflict {
			return &api.RespondSubDAGRunStepAgentInteraction409JSONResponse{Code: api.ErrorCodeConflict, Message: err.Error()}, nil
		}
		return &api.RespondSubDAGRunStepAgentInteraction400JSONResponse{Code: api.ErrorCodeBadRequest, Message: err.Error()}, nil
	}
	return (*api.RespondSubDAGRunStepAgentInteraction200JSONResponse)(&response), nil
}

// RestartDAGRunStepAgentSession discards a lost session and queues a clean run.
func (a *API) RestartDAGRunStepAgentSession(ctx context.Context, request api.RestartDAGRunStepAgentSessionRequestObject) (api.RestartDAGRunStepAgentSessionResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}
	response, err := a.restartAgentSession(ctx, ir.NewDAGRunRef(request.Name, request.DagRunId), "", request.StepName)
	if err != nil {
		var actionErr *agentSessionActionError
		if errors.As(err, &actionErr) && actionErr.notFound {
			return &api.RestartDAGRunStepAgentSession404JSONResponse{Code: api.ErrorCodeNotFound, Message: err.Error()}, nil
		}
		if errors.As(err, &actionErr) && actionErr.conflict {
			return &api.RestartDAGRunStepAgentSession409JSONResponse{Code: api.ErrorCodeConflict, Message: err.Error()}, nil
		}
		return &api.RestartDAGRunStepAgentSession400JSONResponse{Code: api.ErrorCodeBadRequest, Message: err.Error()}, nil
	}
	return (*api.RestartDAGRunStepAgentSession200JSONResponse)(&response), nil
}

// RestartSubDAGRunStepAgentSession discards a lost sub DAG-run session and queues a clean run.
func (a *API) RestartSubDAGRunStepAgentSession(ctx context.Context, request api.RestartSubDAGRunStepAgentSessionRequestObject) (api.RestartSubDAGRunStepAgentSessionResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}
	response, err := a.restartAgentSession(ctx, ir.NewDAGRunRef(request.Name, request.DagRunId), request.SubDAGRunId, request.StepName)
	if err != nil {
		var actionErr *agentSessionActionError
		if errors.As(err, &actionErr) && actionErr.notFound {
			return &api.RestartSubDAGRunStepAgentSession404JSONResponse{Code: api.ErrorCodeNotFound, Message: err.Error()}, nil
		}
		if errors.As(err, &actionErr) && actionErr.conflict {
			return &api.RestartSubDAGRunStepAgentSession409JSONResponse{Code: api.ErrorCodeConflict, Message: err.Error()}, nil
		}
		return &api.RestartSubDAGRunStepAgentSession400JSONResponse{Code: api.ErrorCodeBadRequest, Message: err.Error()}, nil
	}
	return (*api.RestartSubDAGRunStepAgentSession200JSONResponse)(&response), nil
}

func (a *API) loadAgentStatus(ctx context.Context, root ir.DAGRunRef, subDAGRunID string) (ir.DAGRunRef, *ir.DAGRunStatus, dagrun.Attempt, error) {
	var (
		mutationRef ir.DAGRunRef
		status      *ir.DAGRunStatus
		attempt     dagrun.Attempt
		err         error
	)
	if subDAGRunID == "" {
		mutationRef = root
		status, err = a.dagRunMgr.GetSavedStatus(ctx, root)
		if err == nil {
			attempt, err = a.dagRunRepository.FindAttempt(ctx, root)
		}
	} else {
		mutationRef, status, err = a.getReferencedDAGRunStatusWithRef(ctx, root, subDAGRunID, "")
		if err == nil {
			attempt, err = a.getReferencedAttempt(ctx, root, subDAGRunID, status.Name)
		}
	}
	if err != nil {
		return ir.DAGRunRef{}, nil, nil, &agentSessionActionError{notFound: true, message: "DAG-run not found"}
	}
	workspaceName, err := workspaceNameForAttempt(ctx, attempt)
	if err != nil {
		return ir.DAGRunRef{}, nil, nil, err
	}
	if err := a.requireWorkspaceVisible(ctx, workspaceName); err != nil {
		return ir.DAGRunRef{}, nil, nil, err
	}
	return mutationRef, status, attempt, nil
}

func (a *API) requireAgentOwnerAvailable(ctx context.Context, ref ir.DAGRunRef, status *ir.DAGRunStatus, stepName string) error {
	node, err := agentSessionNode(status, stepName)
	if err != nil {
		return err
	}
	workerID := node.AgentSession.OwnerWorkerID
	if workerID == "" || workerID == "local" {
		message := "The server that owns this OpenCode session is unavailable; the interaction remains pending"
		if a.openCodeHost != nil {
			hostConfig, hostErr := a.openCodeHost.Ensure(ctx)
			if hostErr != nil {
				return &agentSessionActionError{conflict: true, message: "The managed OpenCode service is temporarily unavailable; the interaction remains pending"}
			}
			available, probeErr := opencodehost.SessionAvailable(ctx, hostConfig, node.AgentSession.Directory, node.AgentSession.SessionID)
			if probeErr != nil {
				return &agentSessionActionError{conflict: true, message: "The managed OpenCode session could not be verified; the interaction remains pending"}
			}
			if available {
				return nil
			}
		}
		_ = a.markAgentSessionUnavailable(ctx, ref, stepName, message)
		return &agentSessionActionError{conflict: true, message: message}
	}
	message := "The worker that owns this OpenCode session is unavailable; the interaction remains pending"
	if a.workerHeartbeatStore == nil {
		_ = a.markAgentSessionUnavailable(ctx, ref, stepName, message)
		return &agentSessionActionError{conflict: true, message: message}
	}
	record, heartbeatErr := a.workerHeartbeatStore.Get(ctx, workerID)
	if heartbeatErr == nil && record != nil && time.Since(record.LastHeartbeatTime()) < managedAgentOwnerStaleThreshold {
		return nil
	}
	if heartbeatErr != nil && !errors.Is(heartbeatErr, dispatch.ErrWorkerHeartbeatNotFound) {
		return &agentSessionActionError{conflict: true, message: "The owning worker could not be verified; the interaction remains pending"}
	}
	_ = a.markAgentSessionUnavailable(ctx, ref, stepName, message)
	return &agentSessionActionError{conflict: true, message: message}
}

func (a *API) markAgentSessionUnavailable(ctx context.Context, ref ir.DAGRunRef, stepName, message string) error {
	status, err := a.dagRunMgr.GetSavedStatus(ctx, ref)
	if err != nil {
		return err
	}
	_, swapped, err := a.compareAndSwapManualStatus(ctx, ref, status, func(latest *ir.DAGRunStatus) error {
		node, nodeErr := agentSessionNode(latest, stepName)
		if nodeErr != nil {
			return nodeErr
		}
		node.AgentSession.State = ir.AgentSessionUnavailable
		node.AgentSession.LastError = message
		appendAgentAPIEvent(node.AgentSession, "lifecycle", "unavailable", message)
		return nil
	})
	if err != nil {
		return err
	}
	if !swapped {
		return errors.New("DAG-run state changed while marking the managed-agent owner unavailable")
	}
	return nil
}

func isAgentOwnerDispatchUnavailable(err error) bool {
	code := grpcstatus.Code(err)
	return code == codes.FailedPrecondition || code == codes.Unavailable
}

func (a *API) respondAgentInteraction(ctx context.Context, root ir.DAGRunRef, subDAGRunID, stepName, interactionID string, body *api.AgentInteractionResponseRequest) (api.AgentInteractionResponse, error) {
	mutationRef, status, attempt, err := a.loadAgentStatus(ctx, root, subDAGRunID)
	if err != nil {
		return api.AgentInteractionResponse{}, err
	}
	status, err = a.waitForManualStepMutationReady(ctx, attempt, status)
	if err != nil {
		return api.AgentInteractionResponse{}, err
	}
	if err := a.requireAgentOwnerAvailable(ctx, mutationRef, status, stepName); err != nil {
		return api.AgentInteractionResponse{}, err
	}
	original, err := cloneManualStatus(status)
	if err != nil {
		return api.AgentInteractionResponse{}, err
	}
	updated, swapped, err := a.compareAndSwapManualStatus(ctx, mutationRef, status, func(latest *ir.DAGRunStatus) error {
		node, err := agentSessionNode(latest, stepName)
		if err != nil {
			return err
		}
		return applyAgentInteractionResponse(ctx, node, interactionID, body)
	})
	if err != nil {
		return api.AgentInteractionResponse{}, err
	}
	if !swapped {
		return api.AgentInteractionResponse{}, &agentSessionActionError{conflict: true, message: "DAG-run state changed before the interaction response could be stored"}
	}
	applied, err := cloneManualStatus(updated)
	if err != nil {
		return api.AgentInteractionResponse{}, err
	}
	resumed := !hasWaitingSteps(updated.Nodes)
	if resumed {
		if subDAGRunID == "" {
			err = a.resumeDAGRun(ctx, root, root.ID)
		} else {
			err = a.resumeSubDAGRun(ctx, root, subDAGRunID)
		}
		if err != nil {
			_ = a.rollbackPushBack(ctx, mutationRef, applied, original)
			if isAgentOwnerDispatchUnavailable(err) {
				_ = a.markAgentSessionUnavailable(ctx, mutationRef, stepName, "The worker that owns this OpenCode session is unavailable")
				return api.AgentInteractionResponse{}, &agentSessionActionError{conflict: true, message: "The worker that owns this OpenCode session is unavailable; the response remains pending"}
			}
			return api.AgentInteractionResponse{}, fmt.Errorf("resume managed-agent session: %w", err)
		}
	}
	a.logAudit(ctx, audit.CategoryDAG, "dag_agent_interaction_respond", map[string]any{
		"dag_name": root.Name, "dag_run_id": root.ID, "sub_dag_run_id": subDAGRunID,
		"step": stepName, "interaction_id": interactionID,
	})
	return api.AgentInteractionResponse{
		DagRunId: responseDAGRunID(root.ID, subDAGRunID), StepName: stepName, InteractionId: interactionID,
		Resumed: resumed, SubDAGRunId: optionalAgentSubRunID(subDAGRunID),
	}, nil
}

func (a *API) restartAgentSession(ctx context.Context, root ir.DAGRunRef, subDAGRunID, stepName string) (api.AgentSessionRestartResponse, error) {
	mutationRef, status, attempt, err := a.loadAgentStatus(ctx, root, subDAGRunID)
	if err != nil {
		return api.AgentSessionRestartResponse{}, err
	}
	status, err = a.waitForManualStepMutationReady(ctx, attempt, status)
	if err != nil {
		return api.AgentSessionRestartResponse{}, err
	}
	original, err := cloneManualStatus(status)
	if err != nil {
		return api.AgentSessionRestartResponse{}, err
	}
	updated, swapped, err := a.compareAndSwapManualStatus(ctx, mutationRef, status, func(latest *ir.DAGRunStatus) error {
		node, err := agentSessionNode(latest, stepName)
		if err != nil {
			return err
		}
		return applyAgentSessionRestart(node)
	})
	if err != nil {
		return api.AgentSessionRestartResponse{}, err
	}
	if !swapped {
		return api.AgentSessionRestartResponse{}, &agentSessionActionError{conflict: true, message: "DAG-run state changed before the agent session could be restarted"}
	}
	applied, err := cloneManualStatus(updated)
	if err != nil {
		return api.AgentSessionRestartResponse{}, err
	}
	node, err := agentSessionNode(updated, stepName)
	if err != nil {
		return api.AgentSessionRestartResponse{}, err
	}
	if subDAGRunID == "" {
		err = a.resumeDAGRun(ctx, root, root.ID)
	} else {
		err = a.resumeSubDAGRun(ctx, root, subDAGRunID)
	}
	if err != nil {
		_ = a.rollbackPushBack(ctx, mutationRef, applied, original)
		if isAgentOwnerDispatchUnavailable(err) {
			return api.AgentSessionRestartResponse{}, &agentSessionActionError{conflict: true, message: "No eligible worker is available to start a clean OpenCode session"}
		}
		return api.AgentSessionRestartResponse{}, fmt.Errorf("restart managed-agent session: %w", err)
	}
	a.logAudit(ctx, audit.CategoryDAG, "dag_agent_session_restart", map[string]any{
		"dag_name": root.Name, "dag_run_id": root.ID, "sub_dag_run_id": subDAGRunID,
		"step": stepName, "generation": node.AgentSession.Generation,
	})
	return api.AgentSessionRestartResponse{
		DagRunId: responseDAGRunID(root.ID, subDAGRunID), StepName: stepName, Generation: node.AgentSession.Generation,
		Resumed: true, SubDAGRunId: optionalAgentSubRunID(subDAGRunID),
	}, nil
}

func agentSessionNode(status *ir.DAGRunStatus, stepName string) (*ir.Node, error) {
	if status == nil {
		return nil, &agentSessionActionError{notFound: true, message: "DAG-run status not found"}
	}
	index := findStepByName(status.Nodes, stepName)
	if index < 0 {
		return nil, &agentSessionActionError{notFound: true, message: fmt.Sprintf("step %s not found", stepName)}
	}
	node := status.Nodes[index]
	if node.AgentSession == nil {
		return nil, &agentSessionActionError{notFound: true, message: fmt.Sprintf("step %s has no managed-agent session", stepName)}
	}
	return node, nil
}

func applyAgentInteractionResponse(ctx context.Context, node *ir.Node, interactionID string, body *api.AgentInteractionResponseRequest) error {
	if node.Status != ir.NodeWaiting || node.AgentSession.State != ir.AgentSessionWaiting {
		return errors.New("managed-agent session is not waiting for input")
	}
	for i := range node.AgentSession.Interactions {
		interaction := &node.AgentSession.Interactions[i]
		if interaction.ID != interactionID {
			continue
		}
		if interaction.Status != ir.AgentInteractionPending {
			return errors.New("managed-agent interaction has already been answered")
		}
		if err := validateAgentInteractionResponse(*interaction, body); err != nil {
			return err
		}
		if body.Decision != nil {
			interaction.Decision = string(*body.Decision)
		}
		if body.Answers != nil {
			interaction.Answers = cloneAgentAnswers(*body.Answers)
		}
		if interaction.Decision == "reject" {
			interaction.Status = ir.AgentInteractionRejected
		} else {
			interaction.Status = ir.AgentInteractionAnswered
		}
		interaction.RespondedAt = time.Now().UTC().Format(time.RFC3339Nano)
		interaction.RespondedBy, interaction.RespondedByID = manualActionSubject(ctx)
		node.Status = ir.NodeNotStarted
		node.Error = ""
		node.FinishedAt = "-"
		appendAgentAPIEvent(node.AgentSession, "interaction.response", "answered", "Managed-agent interaction answered")
		return nil
	}
	return &agentSessionActionError{notFound: true, message: fmt.Sprintf("interaction %s not found", interactionID)}
}

func validateAgentInteractionResponse(interaction ir.AgentInteraction, body *api.AgentInteractionResponseRequest) error {
	if body == nil {
		return errors.New("interaction response is required")
	}
	switch interaction.Kind {
	case ir.AgentInteractionPermission:
		if body.Decision == nil {
			return errors.New("permission response requires a decision")
		}
		decision := string(*body.Decision)
		if decision != "once" && decision != "session" && decision != "reject" {
			return errors.New("permission decision must be once, session, or reject")
		}
		if decision == "session" && len(interaction.AllowForSessionPatterns) == 0 {
			return errors.New("OpenCode did not provide a session permission scope")
		}
	case ir.AgentInteractionQuestion:
		if body.Decision != nil {
			if string(*body.Decision) == "reject" {
				return nil
			}
			return errors.New("question responses accept answers or reject")
		}
		if body.Answers == nil || len(*body.Answers) != len(interaction.Questions) {
			return errors.New("question response must include one answer set per question")
		}
		for i, question := range interaction.Questions {
			answers := (*body.Answers)[i]
			if len(answers) == 0 {
				return fmt.Errorf("question %d requires an answer", i+1)
			}
			if !question.Multiple && len(answers) != 1 {
				return fmt.Errorf("question %d accepts one answer", i+1)
			}
			for _, answer := range answers {
				answer = strings.TrimSpace(answer)
				if answer == "" {
					return fmt.Errorf("question %d contains an empty answer", i+1)
				}
				if !question.Custom && !agentQuestionHasOption(question, answer) {
					return fmt.Errorf("question %d answer %q is not an offered option", i+1, answer)
				}
			}
		}
	default:
		return errors.New("unsupported managed-agent interaction")
	}
	return nil
}

func agentQuestionHasOption(question ir.AgentQuestion, answer string) bool {
	for _, option := range question.Options {
		if option.Label == answer {
			return true
		}
	}
	return false
}

func applyAgentSessionRestart(node *ir.Node) error {
	if node.Status != ir.NodeWaiting && !node.Status.IsDone() {
		return errors.New("managed-agent step is still running")
	}
	session := node.AgentSession
	if session.Provider != "opencode" {
		return errors.New("only managed OpenCode sessions can be restarted")
	}
	if session.Generation < 1 {
		session.Generation = 1
	}
	session.DiscardedSessionID = session.SessionID
	session.DiscardedOwned = session.SessionOwned
	session.Generation++
	session.SessionID = ""
	session.SessionOwned = false
	session.OwnerWorkerID = ""
	session.HostInstanceID = ""
	session.State = ir.AgentSessionStarting
	session.LastError = ""
	session.PromptSent = false
	session.RestartPending = true
	session.PermissionGrants = nil
	session.Interactions = nil
	node.ChatMessages = nil
	node.Status = ir.NodeNotStarted
	node.Error = ""
	node.FinishedAt = "-"
	appendAgentAPIEvent(session, "lifecycle", "restarting", "Starting a clean OpenCode session")
	return nil
}

func appendAgentAPIEvent(session *ir.AgentSession, eventType, status, content string) {
	sequence := int64(1)
	if len(session.Events) > 0 {
		sequence = session.Events[len(session.Events)-1].Sequence + 1
	}
	session.Events = append(session.Events, ir.AgentSessionEvent{
		Sequence: sequence, ID: fmt.Sprintf("dagu-%d-%d", session.Generation, sequence),
		Type: eventType, Status: status, Content: content, Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func cloneAgentAnswers(answers [][]string) [][]string {
	cloned := make([][]string, len(answers))
	for i := range answers {
		cloned[i] = append([]string(nil), answers[i]...)
	}
	return cloned
}

func optionalAgentSubRunID(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func responseDAGRunID(rootDAGRunID, subDAGRunID string) string {
	if subDAGRunID != "" {
		return subDAGRunID
	}
	return rootDAGRunID
}
