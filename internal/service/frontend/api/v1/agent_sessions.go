// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/auth"
)

var (
	errAgentNotAvailable = &Error{
		Code:       api.ErrorCodeNotFound,
		Message:    "Agent feature is not available",
		HTTPStatus: http.StatusNotFound,
	}

	errAgentNotConfigured = &Error{
		Code:       api.ErrorCodeInternalError,
		Message:    "Agent is not configured properly",
		HTTPStatus: http.StatusServiceUnavailable,
	}

	errAgentSessionNotFound = &Error{
		Code:       api.ErrorCodeNotFound,
		Message:    "Session not found",
		HTTPStatus: http.StatusNotFound,
	}

	errAgentBadRequest = &Error{
		Code:       api.ErrorCodeBadRequest,
		Message:    "Invalid request",
		HTTPStatus: http.StatusBadRequest,
	}

	errAgentPromptExpired = &Error{
		Code:       api.ErrorCodeNotFound,
		Message:    "Prompt expired or already answered",
		HTTPStatus: http.StatusGone,
	}

	errAgentProcessFailed = &Error{
		Code:       api.ErrorCodeInternalError,
		Message:    "Failed to process message",
		HTTPStatus: http.StatusInternalServerError,
	}

	errAgentCancelFailed = &Error{
		Code:       api.ErrorCodeInternalError,
		Message:    "Failed to cancel session",
		HTTPStatus: http.StatusInternalServerError,
	}
)

// requireAgent checks that the agent API is available and enabled.
func (a *API) requireAgent(ctx context.Context) error {
	if a.agentAPI == nil || a.agentConfigStore == nil {
		return errAgentNotAvailable
	}
	if !a.agentConfigStore.IsEnabled(ctx) {
		return errAgentNotAvailable
	}
	return nil
}

// extractUserContext extracts user identity and IP from the request context.
func extractUserContext(ctx context.Context) agent.UserIdentity {
	u := agent.UserIdentity{
		UserID:   "admin",
		Username: "admin",
		Role:     auth.RoleAdmin,
	}
	if user, ok := auth.UserFromContext(ctx); ok && user != nil {
		u.UserID = user.ID
		u.Username = user.Username
		u.Role = user.Role
	}
	u.IPAddress, _ = auth.ClientIPFromContext(ctx)
	return u
}

// mapAgentError maps agent sentinel errors to API errors.
func mapAgentError(err error) error {
	switch {
	case errors.Is(err, agent.ErrMessageRequired):
		return errAgentBadRequest
	case errors.Is(err, agent.ErrAgentNotConfigured):
		return errAgentNotConfigured
	case errors.Is(err, agent.ErrSessionNotFound):
		return errAgentSessionNotFound
	case errors.Is(err, agent.ErrInvalidSessionCursor):
		return errAgentBadRequest
	case errors.Is(err, agent.ErrFailedToProcessMessage):
		return errAgentProcessFailed
	case errors.Is(err, agent.ErrFailedToCancel):
		return errAgentCancelFailed
	case errors.Is(err, agent.ErrPromptIDRequired):
		return errAgentBadRequest
	case errors.Is(err, agent.ErrPromptExpired):
		return errAgentPromptExpired
	default:
		return &Error{
			Code:       api.ErrorCodeInternalError,
			Message:    err.Error(),
			HTTPStatus: http.StatusInternalServerError,
		}
	}
}

// CreateAgentSession creates a new agent session with the first message.
func (a *API) CreateAgentSession(ctx context.Context, request api.CreateAgentSessionRequestObject) (api.CreateAgentSessionResponseObject, error) {
	if err := a.requireAgent(ctx); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, errAgentBadRequest
	}

	user := extractUserContext(ctx)
	chatReq := toAgentChatRequest(request.Body)

	sessionID, status, err := a.agentAPI.CreateSession(ctx, user, chatReq)
	if err != nil {
		return nil, mapAgentError(err)
	}

	return api.CreateAgentSession201JSONResponse{
		SessionId: sessionID,
		Status:    status,
	}, nil
}

// ListAgentSessions lists sessions for the current user with pagination.
func (a *API) ListAgentSessions(ctx context.Context, request api.ListAgentSessionsRequestObject) (api.ListAgentSessionsResponseObject, error) {
	if err := a.requireAgent(ctx); err != nil {
		return nil, err
	}

	user := extractUserContext(ctx)
	perPage := valueOf(request.Params.PerPage)
	cursorMode := request.Params.Cursor != nil ||
		(request.Params.PaginationMode != nil && *request.Params.PaginationMode == api.ListAgentSessionsParamsPaginationModeCursor)
	if cursorMode {
		if request.Params.Page != nil {
			return nil, errAgentBadRequest
		}
		cursor := ""
		if request.Params.Cursor != nil {
			cursor = string(*request.Params.Cursor)
		}
		result, err := a.agentAPI.ListSessionsCursor(ctx, user.UserID, cursor, perPage)
		if err != nil {
			return nil, mapAgentError(err)
		}
		return api.ListAgentSessions200JSONResponse{
			Sessions:   toAPISessions(result.Items),
			Pagination: cursorPagination(len(result.Items), result.NextCursor),
			NextCursor: ptrOf(result.NextCursor),
		}, nil
	}

	page := valueOf(request.Params.Page)
	result := a.agentAPI.ListSessionsPaginated(ctx, user.UserID, page, perPage)

	return api.ListAgentSessions200JSONResponse{
		Sessions:   toAPISessions(result.Items),
		Pagination: toPagination(result),
	}, nil
}

func cursorPagination(count int, nextCursor string) api.Pagination {
	totalPages := 1
	nextPage := 1
	if nextCursor != "" {
		totalPages = 2
		nextPage = 2
	}
	return api.Pagination{
		CurrentPage:  1,
		NextPage:     nextPage,
		PrevPage:     1,
		TotalPages:   totalPages,
		TotalRecords: count,
	}
}

// GetAgentSession returns session details including messages and state.
func (a *API) GetAgentSession(ctx context.Context, request api.GetAgentSessionRequestObject) (api.GetAgentSessionResponseObject, error) {
	if err := a.requireAgent(ctx); err != nil {
		return nil, err
	}

	user := extractUserContext(ctx)
	detail, err := a.agentAPI.GetSessionDetail(ctx, request.SessionId, user.UserID)
	if err != nil {
		return nil, mapAgentError(err)
	}

	return api.GetAgentSession200JSONResponse(toAPISessionDetail(detail)), nil
}

// ChatAgentSession sends a message to an existing session.
func (a *API) ChatAgentSession(ctx context.Context, request api.ChatAgentSessionRequestObject) (api.ChatAgentSessionResponseObject, error) {
	if err := a.requireAgent(ctx); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, errAgentBadRequest
	}

	user := extractUserContext(ctx)
	chatReq := toAgentChatRequest(request.Body)

	if err := a.agentAPI.SendMessage(ctx, request.SessionId, user, chatReq); err != nil {
		return nil, mapAgentError(err)
	}

	return api.ChatAgentSession202JSONResponse{Status: "accepted"}, nil
}

// CancelAgentSession cancels an active session.
func (a *API) CancelAgentSession(ctx context.Context, request api.CancelAgentSessionRequestObject) (api.CancelAgentSessionResponseObject, error) {
	if err := a.requireAgent(ctx); err != nil {
		return nil, err
	}

	user := extractUserContext(ctx)
	if err := a.agentAPI.CancelSession(ctx, request.SessionId, user.UserID); err != nil {
		return nil, mapAgentError(err)
	}

	return api.CancelAgentSession200JSONResponse{Status: "cancelled"}, nil
}

// RespondAgentSession submits a user's response to an agent prompt.
func (a *API) RespondAgentSession(ctx context.Context, request api.RespondAgentSessionRequestObject) (api.RespondAgentSessionResponseObject, error) {
	if err := a.requireAgent(ctx); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, errAgentBadRequest
	}

	user := extractUserContext(ctx)
	resp := toAgentUserPromptResponse(request.Body)

	if err := a.agentAPI.SubmitUserResponse(ctx, request.SessionId, user.UserID, resp); err != nil {
		return nil, mapAgentError(err)
	}

	return api.RespondAgentSession200JSONResponse{Status: "accepted"}, nil
}

// --- Conversion functions ---

func toAgentChatRequest(req *api.AgentChatRequest) agent.ChatRequest {
	out := agent.ChatRequest{
		Message: req.Message,
	}
	if req.Model != nil {
		out.Model = *req.Model
	}
	if req.SafeMode != nil {
		out.SafeMode = *req.SafeMode
	}
	if req.SoulId != nil {
		out.SoulID = *req.SoulId
	}
	if req.SessionId != nil {
		out.SessionID = *req.SessionId
	}
	if req.DagContexts != nil {
		for _, dc := range *req.DagContexts {
			ctx := agent.DAGContext{DAGFile: dc.DagFile}
			if dc.DagRunId != nil {
				ctx.DAGRunID = *dc.DagRunId
			}
			out.DAGContexts = append(out.DAGContexts, ctx)
		}
	}
	return out
}

func toAgentUserPromptResponse(req *api.AgentUserPromptResponse) agent.UserPromptResponse {
	out := agent.UserPromptResponse{
		PromptID: req.PromptId,
	}
	if req.SelectedOptionIds != nil {
		out.SelectedOptionIDs = *req.SelectedOptionIds
	}
	if req.FreeTextResponse != nil {
		out.FreeTextResponse = *req.FreeTextResponse
	}
	if req.Cancelled != nil {
		out.Cancelled = *req.Cancelled
	}
	return out
}

func toAPISessions(sessions []agent.SessionWithState) []api.AgentSessionWithState {
	result := make([]api.AgentSessionWithState, len(sessions))
	for i, s := range sessions {
		result[i] = api.AgentSessionWithState{
			Session:          toAPISession(s.Session),
			SessionId:        s.Session.ID,
			Working:          s.Working,
			HasPendingPrompt: ptrOf(s.HasPendingPrompt),
			Model:            ptrOf(s.Model),
			TotalCost:        s.TotalCost,
		}
	}
	return result
}

func toAPISessionDetail(resp *agent.StreamResponse) api.AgentSessionDetailResponse {
	out := api.AgentSessionDetailResponse{}

	if resp.Session != nil {
		out.Session = toAPISession(*resp.Session)
	}
	if resp.SessionState != nil {
		out.SessionState = api.AgentSessionState{
			SessionId:        resp.SessionState.SessionID,
			Working:          resp.SessionState.Working,
			HasPendingPrompt: ptrOf(resp.SessionState.HasPendingPrompt),
			Model:            ptrOf(resp.SessionState.Model),
			TotalCost:        resp.SessionState.TotalCost,
		}
	}
	if resp.Messages != nil {
		out.Messages = toAPIMessages(resp.Messages)
	}
	if len(resp.Delegates) > 0 {
		delegates := make([]api.AgentDelegateSnapshot, len(resp.Delegates))
		for i, d := range resp.Delegates {
			cost := d.Cost
			delegates[i] = api.AgentDelegateSnapshot{
				Id:     d.ID,
				Task:   d.Task,
				Status: api.AgentDelegateSnapshotStatus(d.Status),
				Cost:   &cost,
			}
		}
		out.Delegates = &delegates
	}
	return out
}

func toAPISession(s agent.Session) api.AgentSession {
	return api.AgentSession{
		Id:              s.ID,
		UserId:          ptrOf(s.UserID),
		DagName:         ptrOf(s.DAGName),
		Title:           ptrOf(s.Title),
		CreatedAt:       s.CreatedAt,
		UpdatedAt:       s.UpdatedAt,
		ParentSessionId: ptrOf(s.ParentSessionID),
		DelegateTask:    ptrOf(s.DelegateTask),
	}
}

func toAPIMessages(msgs []agent.Message) []api.AgentMessage {
	result := make([]api.AgentMessage, len(msgs))
	for i, m := range msgs {
		msg := api.AgentMessage{
			Id:          m.ID,
			SessionId:   m.SessionID,
			Type:        api.AgentMessageType(m.Type),
			SequenceId:  m.SequenceID,
			Content:     ptrOf(m.Content),
			CreatedAt:   m.CreatedAt,
			Cost:        m.Cost,
			DelegateIds: ptrOf(m.DelegateIDs),
		}

		if len(m.ToolCalls) > 0 {
			calls := make([]api.AgentToolCall, len(m.ToolCalls))
			for j, tc := range m.ToolCalls {
				calls[j] = api.AgentToolCall{
					Id:   tc.ID,
					Type: tc.Type,
					Function: api.AgentToolCallFunction{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				}
			}
			msg.ToolCalls = &calls
		}

		if len(m.ToolResults) > 0 {
			results := make([]api.AgentToolResult, len(m.ToolResults))
			for j, tr := range m.ToolResults {
				results[j] = api.AgentToolResult{
					ToolCallId: tr.ToolCallID,
					Content:    tr.Content,
					IsError:    ptrOf(tr.IsError),
				}
			}
			msg.ToolResults = &results
		}

		if m.Usage != nil {
			msg.Usage = &api.AgentTokenUsage{
				PromptTokens:     ptrOf(m.Usage.PromptTokens),
				CompletionTokens: ptrOf(m.Usage.CompletionTokens),
				TotalTokens:      ptrOf(m.Usage.TotalTokens),
			}
		}

		if m.UIAction != nil {
			msg.UiAction = &api.AgentUIAction{
				Type: string(m.UIAction.Type),
				Path: ptrOf(m.UIAction.Path),
			}
		}

		if m.UserPrompt != nil {
			prompt := &api.AgentUserPrompt{
				PromptId:            m.UserPrompt.PromptID,
				Question:            m.UserPrompt.Question,
				AllowFreeText:       m.UserPrompt.AllowFreeText,
				FreeTextPlaceholder: ptrOf(m.UserPrompt.FreeTextPlaceholder),
				MultiSelect:         m.UserPrompt.MultiSelect,
				Command:             ptrOf(m.UserPrompt.Command),
				WorkingDir:          ptrOf(m.UserPrompt.WorkingDir),
			}
			if m.UserPrompt.PromptType != "" {
				pt := api.AgentUserPromptPromptType(m.UserPrompt.PromptType)
				prompt.PromptType = &pt
			}
			if len(m.UserPrompt.Options) > 0 {
				opts := make([]api.AgentUserPromptOption, len(m.UserPrompt.Options))
				for j, o := range m.UserPrompt.Options {
					opts[j] = api.AgentUserPromptOption{
						Id:          o.ID,
						Label:       o.Label,
						Description: ptrOf(o.Description),
					}
				}
				prompt.Options = &opts
			}
			msg.UserPrompt = prompt
		}

		result[i] = msg
	}
	return result
}
