// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	api "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/agentoauth"
	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/llm"
	"github.com/dagucloud/dagu/internal/service/eventstore"
	workspacepkg "github.com/dagucloud/dagu/internal/workspace"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// respondErrorDirect writes a JSON error response (for use outside API methods).
func respondErrorDirect(w http.ResponseWriter, status int, code api.ErrorCode, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]string{
		"code":    string(code),
		"message": message,
	}); err != nil {
		slog.Error("failed to encode error response", "error", err)
	}
}

// defaultUserID is used when no user is authenticated (e.g., auth disabled).
// This value should match the system's expected default user identifier.
const defaultUserID = "admin"
const defaultUserRole = auth.RoleAdmin

const (
	defaultBotContextWindow = 200_000
	sessionCompactionRatio  = 0.80
	sessionSummaryPrefix    = "Session handoff summary:\n"
	compactionSummaryPrompt = "You are compressing a Dagu agent session into a continuation handoff. Write a concise markdown bullet summary for the next assistant turn. Include the user's goal, durable preferences or constraints, important DAG/file/runtime facts, recent decisions or outcomes, and unresolved next steps. Do not invent facts. Omit trivial chatter. Keep it under 12 bullets."
)

// getUserIDFromContext extracts the user ID from the request context.
// Returns "admin" if no user is authenticated (e.g., auth mode is "none").
func getUserIDFromContext(ctx context.Context) string {
	if user, ok := auth.UserFromContext(ctx); ok && user != nil {
		return user.ID
	}
	return defaultUserID
}

func getAuthenticatedUserIDFromContext(ctx context.Context) (string, bool) {
	if user, ok := auth.UserFromContext(ctx); ok && user != nil {
		return user.ID, true
	}
	return "", false
}

func userIdentityFromContext(ctx context.Context) UserIdentity {
	user := UserIdentity{
		UserID:          defaultUserID,
		Username:        defaultUserID,
		Role:            defaultUserRole,
		WorkspaceAccess: auth.AllWorkspaceAccess(),
	}
	if authUser, ok := auth.UserFromContext(ctx); ok && authUser != nil {
		user.UserID = authUser.ID
		user.Username = authUser.Username
		user.Role = authUser.Role
		user.WorkspaceAccess = auth.CloneWorkspaceAccess(authUser.WorkspaceAccess)
	}
	user.IPAddress, _ = auth.ClientIPFromContext(ctx)
	return user
}

// API handles HTTP requests for the agent.
type API struct {
	sessions              sync.Map // id -> *SessionManager (active sessions)
	creatingIDs           sync.Map // id -> struct{} (session IDs currently being created)
	spillMu               sync.Mutex
	store                 SessionStore
	configStore           ConfigStore
	modelStore            ModelStore
	providers             *ProviderCache
	workingDir            string
	logger                *slog.Logger
	dagStore              DAGMetadataStore // For resolving DAG file paths
	environment           EnvironmentInfo
	hooks                 *Hooks
	memoryStore           MemoryStore
	docStore              DocStore
	workspaceStore        workspacepkg.Store
	soulStore             SoulStore
	remoteContextResolver RemoteContextResolver
	oauthManager          *agentoauth.Manager
	eventService          *eventstore.Service
}

// APIConfig contains configuration for the API.
type APIConfig struct {
	ConfigStore           ConfigStore
	ModelStore            ModelStore
	SoulStore             SoulStore
	WorkingDir            string
	Logger                *slog.Logger
	SessionStore          SessionStore
	DAGStore              DAGMetadataStore // For resolving DAG file paths
	Environment           EnvironmentInfo
	Hooks                 *Hooks
	MemoryStore           MemoryStore
	DocStore              DocStore
	WorkspaceStore        workspacepkg.Store
	RemoteContextResolver RemoteContextResolver
	OAuthManager          *agentoauth.Manager
	EventService          *eventstore.Service
}

// SessionWithState is a session with its current state.
type SessionWithState struct {
	Session          Session `json:"session"`
	Working          bool    `json:"working"`
	HasPendingPrompt bool    `json:"has_pending_prompt"`
	Model            string  `json:"model,omitempty"`
	TotalCost        float64 `json:"total_cost"`
}

type sessionRuntimeConfig struct {
	modelID         string
	resolvedModel   string
	modelCfg        *ModelConfig
	dagName         string
	title           string
	safeMode        bool
	soul            *Soul
	webSearch       *llm.WebSearchRequest
	thinkingEffort  llm.ThinkingEffort
	inputCostPer1M  float64
	outputCostPer1M float64
}

// NewAPI creates a new API instance.
func NewAPI(cfg APIConfig) *API {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &API{
		configStore:           cfg.ConfigStore,
		modelStore:            cfg.ModelStore,
		soulStore:             cfg.SoulStore,
		providers:             NewProviderCache(),
		workingDir:            cfg.WorkingDir,
		logger:                logger,
		store:                 cfg.SessionStore,
		dagStore:              cfg.DAGStore,
		environment:           cfg.Environment,
		hooks:                 cfg.Hooks,
		memoryStore:           cfg.MemoryStore,
		docStore:              cfg.DocStore,
		workspaceStore:        cfg.WorkspaceStore,
		remoteContextResolver: cfg.RemoteContextResolver,
		oauthManager:          cfg.OAuthManager,
		eventService:          cfg.EventService,
	}
}

// RegisterRoutes registers the agent SSE stream route on the given router.
// All other agent endpoints are served through the OpenAPI handler.
func (a *API) RegisterRoutes(r chi.Router, authMiddleware func(http.Handler) http.Handler) {
	r.Route("/api/v1/agent", func(r chi.Router) {
		r.Use(a.enabledMiddleware())
		if authMiddleware != nil {
			r.Use(authMiddleware)
		}
		r.Get("/sessions/{id}/stream", a.handleStream)
	})
}

// enabledMiddleware returns middleware that checks if agent is enabled.
func (a *API) enabledMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !a.configStore.IsEnabled(r.Context()) {
				respondErrorDirect(w, http.StatusNotFound, api.ErrorCodeNotFound, "Agent feature is disabled")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// resolveContexts resolves DAG file names to full paths using the DAG store.
func (a *API) resolveContexts(ctx context.Context, contexts []DAGContext) []ResolvedDAGContext {
	if len(contexts) == 0 || a.dagStore == nil {
		return nil
	}

	var resolved []ResolvedDAGContext
	for _, c := range contexts {
		if c.DAGFile == "" {
			continue
		}

		dag, err := a.dagStore.GetMetadata(ctx, c.DAGFile)
		if err != nil || dag == nil {
			continue
		}

		resolved = append(resolved, ResolvedDAGContext{
			DAGFilePath: dag.Location,
			DAGName:     dag.Name,
			DAGRunID:    c.DAGRunID,
		})
	}
	return resolved
}

// formatMessageWithContexts prepends DAG context information to the user message.
func formatMessageWithContexts(message string, contexts []ResolvedDAGContext) string {
	if len(contexts) == 0 {
		return message
	}

	var contextLines []string
	for _, ctx := range contexts {
		line := formatContextLine(ctx)
		if line != "" {
			contextLines = append(contextLines, line)
		}
	}

	if len(contextLines) == 0 {
		return message
	}

	return fmt.Sprintf("[Referenced DAGs:\n%s]\n\n%s", strings.Join(contextLines, "\n"), message)
}

// formatContextLine formats a single DAG context as a readable line.
func formatContextLine(ctx ResolvedDAGContext) string {
	var parts []string
	if ctx.DAGFilePath != "" {
		parts = append(parts, "file: "+ctx.DAGFilePath)
	}
	if ctx.DAGRunID != "" {
		parts = append(parts, "run: "+ctx.DAGRunID)
	}
	if ctx.RunStatus != "" {
		parts = append(parts, "status: "+ctx.RunStatus)
	}
	if len(parts) == 0 {
		return ""
	}
	return fmt.Sprintf("- %s (%s)", cmp.Or(ctx.DAGName, "unknown"), strings.Join(parts, ", "))
}

// selectModel returns the first non-empty model from the provided choices,
// falling back to the default model from config.
// Priority: requestModel > sessionModel > config default.
func selectModel(requestModel, sessionModel, configModel string) string {
	return cmp.Or(requestModel, sessionModel, configModel)
}

// getDefaultModelID returns the default model ID from config.
func (a *API) getDefaultModelID(ctx context.Context) string {
	cfg, err := a.configStore.Load(ctx)
	if err != nil {
		a.logger.Warn("Failed to load agent config for default model", "error", err)
		return ""
	}
	return cfg.DefaultModelID
}

// resolveProvider resolves a model ID to an LLM provider and model config.
// If modelID is empty, uses the default from config.
// If the requested model is not found (e.g., deleted), falls back to the default.
func (a *API) resolveProvider(ctx context.Context, modelID string) (llm.Provider, *ModelConfig, error) {
	if a.modelStore == nil {
		return nil, nil, errors.New("model store not configured")
	}

	defaultID := a.getDefaultModelID(ctx)
	modelID = cmp.Or(modelID, defaultID)
	if modelID == "" {
		return nil, nil, errors.New("no model configured")
	}

	model, err := a.modelStore.GetByID(ctx, modelID)
	if errors.Is(err, ErrModelNotFound) && defaultID != "" && defaultID != modelID {
		// Requested model was deleted; fall back to default
		model, err = a.modelStore.GetByID(ctx, defaultID)
	}
	if err != nil {
		return nil, nil, err
	}

	provider, _, err := a.providers.GetOrCreate(model.ToLLMConfig(), ProviderDeps{
		OAuthManager: a.oauthManager,
	})
	if err != nil {
		return nil, nil, err
	}
	return provider, model, nil
}

func (a *API) loadWebSearch(ctx context.Context) *llm.WebSearchRequest {
	cfg, err := a.configStore.Load(ctx)
	if err != nil || cfg == nil || cfg.WebSearch == nil || !cfg.WebSearch.Enabled {
		return nil
	}
	return &llm.WebSearchRequest{
		Enabled: true,
		MaxUses: cfg.WebSearch.MaxUses,
	}
}

// loadSoulWithOverride loads a soul by explicit ID override, falling back to the
// global default from config. Use this for session creation where the client may
// specify a soul.
func (a *API) loadSoulWithOverride(ctx context.Context, overrideID string) *Soul {
	if overrideID != "" && a.soulStore != nil {
		soul, err := a.soulStore.GetByID(ctx, overrideID)
		if err == nil {
			return soul
		}
		a.logger.Debug("override soul not found, falling back to default", "soulId", overrideID, "error", err)
	}
	return a.loadSelectedSoul(ctx)
}

// loadSelectedSoul returns the selected soul from the agent config.
// Falls back to "default" soul if the configured soul is not found.
// Returns nil if no soul store is configured or no soul is available.
func (a *API) loadSelectedSoul(ctx context.Context) *Soul {
	if a.soulStore == nil {
		return nil
	}

	cfg, err := a.configStore.Load(ctx)
	if err != nil || cfg == nil {
		a.logger.Debug("failed to load agent config for soul selection", "error", err)
		return nil
	}

	soulID := cfg.SelectedSoulID
	if soulID == "" {
		soulID = "default"
	}

	soul, err := a.soulStore.GetByID(ctx, soulID)
	if err == nil {
		return soul
	}

	// If we already tried "default", nothing else to try.
	if soulID == "default" {
		a.logger.Debug("default soul not found", "error", err)
		return nil
	}

	// Fall back to "default".
	a.logger.Debug("selected soul not found, falling back to default", "soulId", soulID, "error", err)
	soul, err = a.soulStore.GetByID(ctx, "default")
	if err != nil {
		a.logger.Debug("default soul not found", "error", err)
		return nil
	}
	return soul
}

// createMessageCallback returns a persistence callback for the given session ID.
// Returns nil if no store is configured.
func (a *API) createMessageCallback(id string) func(ctx context.Context, msg Message) error {
	if a.store == nil {
		return nil
	}
	return func(ctx context.Context, msg Message) error {
		if err := a.store.AddMessage(ctx, id, &msg); err != nil {
			return err
		}
		if a.eventService != nil && msg.Type == MessageTypeAssistant && msg.Usage != nil {
			source := eventstore.Source{
				Service: eventstore.SourceServiceServer,
			}
			model, userID := a.lookupSessionEventMetadata(id)
			if err := a.eventService.Emit(ctx, eventstore.NewLLMUsageEvent(
				source,
				id,
				userID,
				model,
				msg.ID,
				msg.CreatedAt,
				msg.Usage,
				msg.Cost,
			)); err != nil {
				a.logger.Warn("Failed to emit LLM usage event", "session_id", id, "error", err)
			}
		}
		return nil
	}
}

func (a *API) lookupSessionEventMetadata(id string) (model string, userID string) {
	if val, ok := a.sessions.Load(id); ok {
		if mgr, ok := val.(*SessionManager); ok {
			return mgr.GetModel(), mgr.UserID()
		}
	}
	return "", ""
}

// persistNewSession saves a new session to the store if configured.
func (a *API) persistNewSession(ctx context.Context, id, userID, dagName, model string, now time.Time) {
	if a.store == nil {
		return
	}
	sess := &Session{
		ID:        id,
		UserID:    userID,
		DAGName:   dagName,
		Model:     model,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := a.store.CreateSession(ctx, sess); err != nil {
		a.logger.Warn("Failed to persist session", "error", err)
	}
}

func (a *API) persistSessionModel(ctx context.Context, mgr *SessionManager, model string) {
	if mgr == nil {
		return
	}
	if mgr.GetModel() == model {
		return
	}
	mgr.SetModel(model)
	if a.store == nil {
		return
	}
	sess := mgr.GetSession()
	sess.Model = model
	if err := a.store.UpdateSession(ctx, &sess); err != nil {
		a.logger.Warn("Failed to persist session model", "error", err, "session_id", sess.ID, "model", model)
	}
}

func (a *API) loadMemoryContent(ctx context.Context, dagName string) MemoryContent {
	if a.memoryStore == nil {
		return MemoryContent{}
	}
	global, err := a.memoryStore.LoadGlobalMemory(ctx)
	if err != nil {
		a.logger.Debug("failed to load global memory", "error", err)
	}
	var dagMemory string
	if dagName != "" {
		dagMemory, err = a.memoryStore.LoadDAGMemory(ctx, dagName)
		if err != nil {
			a.logger.Debug("failed to load DAG memory", "error", err, "dag_name", dagName)
		}
	}
	return MemoryContent{
		GlobalMemory: global,
		DAGMemory:    dagMemory,
		DAGName:      dagName,
		MemoryDir:    a.memoryStore.MemoryDir(),
	}
}

func cloneWebSearchRequest(req *llm.WebSearchRequest) *llm.WebSearchRequest {
	if req == nil {
		return nil
	}
	out := *req
	if req.AllowedDomains != nil {
		out.AllowedDomains = append([]string(nil), req.AllowedDomains...)
	}
	if req.BlockedDomains != nil {
		out.BlockedDomains = append([]string(nil), req.BlockedDomains...)
	}
	if req.UserLocation != nil {
		loc := *req.UserLocation
		out.UserLocation = &loc
	}
	return &out
}

func modelThinkingEffort(modelCfg *ModelConfig) llm.ThinkingEffort {
	if modelCfg == nil || !modelCfg.SupportsThinking {
		return ""
	}
	effort, err := llm.ParseThinkingEffort(modelCfg.ThinkingEffort)
	if err != nil {
		return ""
	}
	return effort
}

func (a *API) defaultSessionRuntime(ctx context.Context, dagName string, safeMode bool) (sessionRuntimeConfig, error) {
	modelID := a.getDefaultModelID(ctx)
	if modelID == "" {
		return sessionRuntimeConfig{}, ErrAgentNotConfigured
	}
	_, modelCfg, err := a.resolveProvider(ctx, modelID)
	if err != nil {
		return sessionRuntimeConfig{}, ErrAgentNotConfigured
	}
	return sessionRuntimeConfig{
		modelID:         modelID,
		resolvedModel:   modelCfg.Model,
		modelCfg:        modelCfg,
		dagName:         dagName,
		safeMode:        safeMode,
		soul:            a.loadSelectedSoul(ctx),
		webSearch:       cloneWebSearchRequest(a.loadWebSearch(ctx)),
		thinkingEffort:  modelThinkingEffort(modelCfg),
		inputCostPer1M:  modelCfg.InputCostPer1M,
		outputCostPer1M: modelCfg.OutputCostPer1M,
	}, nil
}

func (a *API) runtimeConfigForSession(ctx context.Context, mgr *SessionManager, overrideDAGName string) (sessionRuntimeConfig, error) {
	modelID := selectModel("", mgr.GetModel(), a.getDefaultModelID(ctx))
	if modelID == "" {
		return sessionRuntimeConfig{}, ErrAgentNotConfigured
	}
	_, modelCfg, err := a.resolveProvider(ctx, modelID)
	if err != nil {
		return sessionRuntimeConfig{}, ErrAgentNotConfigured
	}
	return sessionRuntimeConfig{
		modelID:         modelID,
		resolvedModel:   modelCfg.Model,
		modelCfg:        modelCfg,
		dagName:         cmp.Or(overrideDAGName, mgr.dagName),
		title:           mgr.title,
		safeMode:        mgr.safeMode,
		soul:            mgr.soul,
		webSearch:       cloneWebSearchRequest(mgr.webSearch),
		thinkingEffort:  modelThinkingEffort(modelCfg),
		inputCostPer1M:  modelCfg.InputCostPer1M,
		outputCostPer1M: modelCfg.OutputCostPer1M,
	}, nil
}

func (a *API) buildSessionManagerConfig(id string, user UserIdentity, cfg sessionRuntimeConfig) SessionManagerConfig {
	return SessionManagerConfig{
		ID:                    id,
		User:                  user,
		Model:                 cfg.modelID,
		Logger:                a.logger,
		WorkingDir:            a.workingDir,
		Title:                 cfg.title,
		OnMessage:             a.createMessageCallback(id),
		Environment:           a.environment,
		SafeMode:              cfg.safeMode,
		Hooks:                 a.hooks,
		InputCostPer1M:        cfg.inputCostPer1M,
		OutputCostPer1M:       cfg.outputCostPer1M,
		MemoryStore:           a.memoryStore,
		DocStore:              a.docStore,
		WorkspaceStore:        a.workspaceStore,
		DAGName:               cfg.dagName,
		SessionStore:          a.store,
		Soul:                  cfg.soul,
		WebSearch:             cfg.webSearch,
		ThinkingEffort:        cfg.thinkingEffort,
		RemoteContextResolver: a.remoteContextResolver,
	}
}

func (a *API) newManagedSession(ctx context.Context, id string, user UserIdentity, cfg sessionRuntimeConfig, now time.Time) *SessionManager {
	mgr := NewSessionManager(a.buildSessionManagerConfig(id, user, cfg))
	mgr.registry = &sessionRegistry{sessions: &a.sessions, parent: mgr}

	a.persistNewSession(ctx, id, user.UserID, cfg.dagName, cfg.modelID, now)
	a.sessions.Store(id, mgr)

	return mgr
}

func (a *API) ensureSessionLoop(mgr *SessionManager, provider llm.Provider, cfg sessionRuntimeConfig) error {
	if mgr == nil {
		return errors.New("session manager is required")
	}
	if provider == nil {
		return errors.New("LLM provider is required")
	}
	return mgr.ensureLoop(provider, cfg.modelID, cfg.resolvedModel)
}

func (a *API) buildSystemPrompt(ctx context.Context, role auth.Role, dagName string, soul *Soul) string {
	return GenerateSystemPrompt(SystemPromptParams{
		Env:    a.environment,
		Memory: a.loadMemoryContent(ctx, dagName),
		Role:   role,
		Soul:   soul,
	})
}

func (a *API) runOneShotPrompt(ctx context.Context, provider llm.Provider, model, systemPrompt, prompt string) (*llm.ChatResponse, error) {
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: systemPrompt},
		{Role: llm.RoleUser, Content: prompt},
	}

	llmCtx, cancel := context.WithTimeout(ctx, llmRequestTimeout)
	defer cancel()

	return llm.ChatWithRetry(llmCtx, provider, &llm.ChatRequest{
		Model:    model,
		Messages: messages,
	}, llm.DefaultLogicalRetryConfig())
}

func latestPromptTokens(messages []Message) int {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Type != MessageTypeAssistant || msg.Usage == nil {
			continue
		}
		if msg.Usage.PromptTokens > 0 {
			return msg.Usage.PromptTokens
		}
		if msg.Usage.TotalTokens > 0 {
			return msg.Usage.TotalTokens
		}
	}
	return 0
}

func shouldCompactMessages(messages []Message, contextWindow int) bool {
	if contextWindow <= 0 {
		contextWindow = defaultBotContextWindow
	}
	promptTokens := latestPromptTokens(messages)
	if promptTokens == 0 {
		return false
	}
	return promptTokens >= int(float64(contextWindow)*sessionCompactionRatio)
}

func buildCompactionTranscript(messages []Message) string {
	var b strings.Builder
	for _, msg := range messages {
		switch msg.Type {
		case MessageTypeUser:
			if msg.Content == "" || len(msg.ToolResults) > 0 {
				continue
			}
			fmt.Fprintf(&b, "User: %s\n\n", msg.Content)
		case MessageTypeAssistant:
			if msg.Content == "" {
				continue
			}
			fmt.Fprintf(&b, "Assistant: %s\n\n", msg.Content)
		case MessageTypeError:
			if msg.Content == "" {
				continue
			}
			fmt.Fprintf(&b, "Error: %s\n\n", msg.Content)
		case MessageTypeUserPrompt:
			if msg.UserPrompt == nil {
				continue
			}
			fmt.Fprintf(&b, "Prompt: %s\n\n", msg.UserPrompt.Question)
		case MessageTypeUIAction:
			continue
		}
	}
	return strings.TrimSpace(b.String())
}

// formatMessage resolves DAG contexts and formats the message with context information.
func (a *API) formatMessage(ctx context.Context, message string, contexts []DAGContext) string {
	resolved := a.resolveContexts(ctx, contexts)
	return formatMessageWithContexts(message, resolved)
}

// collectActiveSessions gathers active sessions for a user.
func (a *API) collectActiveSessions(userID string, activeIDs map[string]struct{}) []SessionWithState {
	var sessions []SessionWithState

	a.sessions.Range(func(key, value any) bool {
		mgr, ok := value.(*SessionManager)
		if !ok {
			return true // skip invalid entry
		}
		if mgr.UserID() != userID {
			return true
		}

		sess := mgr.GetSession()
		// Skip sub-sessions (delegate sessions) from the main listing.
		if sess.ParentSessionID != "" {
			return true
		}

		id, ok := key.(string)
		if !ok {
			return true // skip invalid key
		}
		activeIDs[id] = struct{}{}
		sessions = append(sessions, SessionWithState{
			Session:          sess,
			Working:          mgr.IsWorking(),
			HasPendingPrompt: mgr.HasPendingPrompt(),
			Model:            mgr.GetModel(),
			TotalCost:        mgr.GetTotalCost(),
		})
		return true
	})

	return sessions
}

// appendPersistedSessions adds non-active persisted sessions to the list.
func (a *API) appendPersistedSessions(ctx context.Context, userID string, activeIDs map[string]struct{}, sessions []SessionWithState) []SessionWithState {
	if a.store == nil {
		return sessions
	}

	persisted, err := a.store.ListSessions(ctx, userID)
	if err != nil {
		a.logger.Warn("Failed to list persisted sessions", "error", err)
		return sessions
	}

	for _, sess := range persisted {
		if _, exists := activeIDs[sess.ID]; exists {
			continue
		}
		// Exclude sub-sessions (delegate sessions) from the main listing.
		if sess.ParentSessionID != "" {
			continue
		}
		sessions = append(sessions, SessionWithState{
			Session:   *sess,
			Working:   false,
			TotalCost: a.getStoredSessionCost(ctx, sess.ID),
		})
	}

	return sessions
}

// getStoredSessionCost sums the cost of all messages in a stored session,
// including costs from delegate sub-sessions.
func (a *API) getStoredSessionCost(ctx context.Context, sessionID string) float64 {
	if a.store == nil {
		return 0
	}
	messages, err := a.store.GetMessages(ctx, sessionID)
	if err != nil {
		return 0
	}
	return sumMessageCosts(messages) + a.sumSubSessionCosts(ctx, sessionID)
}

// sumMessageCosts sums the Cost field across a slice of messages.
func sumMessageCosts(messages []Message) float64 {
	var total float64
	for _, msg := range messages {
		if msg.Cost != nil {
			total += *msg.Cost
		}
	}
	return total
}

// sumSubSessionCosts returns the total cost from all direct sub-session messages.
func (a *API) sumSubSessionCosts(ctx context.Context, sessionID string) float64 {
	if a.store == nil {
		return 0
	}
	subSessions, err := a.store.ListSubSessions(ctx, sessionID)
	if err != nil {
		return 0
	}
	var total float64
	for _, sub := range subSessions {
		subMessages, err := a.store.GetMessages(ctx, sub.ID)
		if err != nil {
			continue
		}
		total += sumMessageCosts(subMessages)
	}
	return total
}

// sumSessionMessageCosts loads messages for a single session and sums their costs.
func (a *API) sumSessionMessageCosts(ctx context.Context, sessionID string) float64 {
	if a.store == nil {
		return 0
	}
	messages, err := a.store.GetMessages(ctx, sessionID)
	if err != nil {
		return 0
	}
	return sumMessageCosts(messages)
}

// getActiveSession retrieves an active session if it exists and belongs to the user.
func (a *API) getActiveSession(id, userID string) (*SessionManager, bool) {
	mgrValue, ok := a.sessions.Load(id)
	if !ok {
		return nil, false
	}
	mgr, ok := mgrValue.(*SessionManager)
	if !ok {
		return nil, false
	}
	if mgr.UserID() != userID {
		return nil, false
	}
	return mgr, true
}

// getStoredSessionMetadata retrieves session metadata from the store.
func (a *API) getStoredSessionMetadata(ctx context.Context, id string) (*Session, error) {
	if a.store == nil {
		return nil, ErrSessionNotFound
	}

	sess, err := a.store.GetSession(ctx, id)
	if err != nil {
		return nil, err
	}
	if sess == nil {
		return nil, ErrSessionNotFound
	}

	return sess, nil
}

// getStoredSession retrieves a session from the store if it exists and belongs to the user.
func (a *API) getStoredSession(ctx context.Context, id, userID string) (*Session, []Message, error) {
	sess, err := a.getStoredSessionMetadata(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if sess.UserID != userID {
		return nil, nil, ErrSessionNotFound
	}

	messages, err := a.store.GetMessages(ctx, id)
	if err != nil {
		return nil, nil, err
	}

	return sess, messages, nil
}

// getOrReactivateSession retrieves an active session or reactivates it from storage.
func (a *API) getOrReactivateSession(ctx context.Context, id string, user UserIdentity) (*SessionManager, bool) {
	// Check active sessions first
	if mgr, ok := a.getActiveSession(id, user.UserID); ok {
		return mgr, true
	}

	// Try to reactivate from store
	return a.reactivateSession(ctx, id, user)
}

// reactivateSession restores a session from storage and makes it active.
func (a *API) reactivateSession(ctx context.Context, id string, user UserIdentity) (*SessionManager, bool) {
	if a.store == nil {
		return nil, false
	}

	sess, err := a.store.GetSession(ctx, id)
	if err != nil || sess == nil || sess.UserID != user.UserID {
		return nil, false
	}

	// Prevent reactivating sub-sessions as top-level sessions.
	if sess.ParentSessionID != "" {
		return nil, false
	}

	messages, err := a.store.GetMessages(ctx, id)
	if err != nil {
		a.logger.Warn("Failed to load messages for reactivation", "error", err)
		messages = []Message{}
	}

	seqID, err := a.store.GetLatestSequenceID(ctx, id)
	if err != nil {
		seqID = int64(len(messages))
	}

	delegates, err := a.getStoredDelegateSnapshots(ctx, id)
	if err != nil {
		a.logger.Warn("Failed to load delegates for reactivation", "error", err)
		delegates = nil
	}

	// Restore pricing and reasoning from the session's persisted model.
	var inputCost, outputCost float64
	var thinkingEffort llm.ThinkingEffort
	modelID := selectModel("", sess.Model, a.getDefaultModelID(ctx))
	if modelID != "" {
		if _, modelCfg, err := a.resolveProvider(ctx, modelID); err == nil {
			inputCost = modelCfg.InputCostPer1M
			outputCost = modelCfg.OutputCostPer1M
			thinkingEffort = modelThinkingEffort(modelCfg)
		}
	}

	cfg := a.buildSessionManagerConfig(id, user, sessionRuntimeConfig{
		modelID:         modelID,
		title:           sess.Title,
		dagName:         sess.DAGName,
		safeMode:        true, // Default to safe mode for reactivated sessions
		soul:            a.loadSelectedSoul(ctx),
		webSearch:       a.loadWebSearch(ctx),
		thinkingEffort:  thinkingEffort,
		inputCostPer1M:  inputCost,
		outputCostPer1M: outputCost,
	})
	cfg.CreatedAt = sess.CreatedAt
	cfg.LastActivity = time.Now()
	cfg.History = messages
	cfg.SequenceID = seqID
	cfg.Delegates = delegates

	mgr := NewSessionManager(cfg)
	mgr.registry = &sessionRegistry{sessions: &a.sessions, parent: mgr}
	a.sessions.Store(id, mgr)

	return mgr, true
}

// handleStream provides SSE streaming for session updates.
// GET /api/v1/agent/sessions/{id}/stream
func (a *API) handleStream(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	user := userIdentityFromContext(r.Context())

	mgr, ok := a.getOrReactivateSession(r.Context(), id, user)
	if !ok {
		respondErrorDirect(w, http.StatusNotFound, api.ErrorCodeNotFound, "Session not found")
		return
	}

	a.setupSSEHeaders(w)
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	// Use atomic subscribe+snapshot to prevent race condition
	// where messages could be missed between getting initial state and subscribing
	snapshot, next := mgr.SubscribeWithSnapshot(r.Context())
	a.sendSSEMessage(w, snapshot)

	type streamResult struct {
		resp StreamResponse
		cont bool
	}

	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()

	done := make(chan struct{})
	defer close(done)

	ch := make(chan streamResult, 1)
	go func() {
		for {
			resp, cont := next()
			select {
			case ch <- streamResult{resp, cont}:
			case <-done:
				return
			}
			if !cont {
				return
			}
		}
	}()

	for {
		select {
		case res := <-ch:
			if !res.cont {
				return
			}
			a.sendSSEMessage(w, res.resp)
			heartbeat.Reset(heartbeatInterval)
		case <-heartbeat.C:
			// SSE comment as heartbeat to keep connection alive
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				a.logger.Debug("SSE heartbeat write failed", "error", err)
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		case <-r.Context().Done():
			return
		}
	}
}

// setupSSEHeaders configures the response headers for Server-Sent Events.
// Note: CORS headers are typically handled by middleware at the router level.
// If SSE-specific CORS headers are needed, configure them via the server config.
func (a *API) setupSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	// CORS headers are managed by the server's CORS middleware configuration.
	// Do not set Access-Control-Allow-Origin here to avoid security issues.
}

// sendSSEMessage sends a single SSE message to the client.
func (a *API) sendSSEMessage(w http.ResponseWriter, data any) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		slog.Error("failed to marshal SSE data", "error", err)
		return
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", jsonData); err != nil {
		a.logger.Debug("SSE write failed", "error", err)
		return
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// ToolInfo contains metadata for a registered agent tool.
type ToolInfo struct {
	Name        string
	Label       string
	Description string
}

// ListTools returns metadata for all registered tools.
func (a *API) ListTools() []ToolInfo {
	regs := RegisteredTools()
	result := make([]ToolInfo, len(regs))
	for i, reg := range regs {
		result[i] = ToolInfo{Name: reg.Name, Label: reg.Label, Description: reg.Description}
	}
	return result
}

// CreateSession creates a new session with the first message.
// Returns the session ID, creation status ("accepted" or "already_exists"), and any error.
// If req.SessionID is provided, the session is created idempotently.
func (a *API) CreateSession(ctx context.Context, user UserIdentity, req ChatRequest) (string, string, error) {
	if req.Message == "" {
		return "", "", ErrMessageRequired
	}

	// Handle idempotent session creation.
	id := req.SessionID
	if id != "" {
		if !isValidUUID(id) {
			return "", "", fmt.Errorf("bad request: sessionId must be a valid UUID")
		}

		// Atomically claim this ID first to prevent TOCTOU races.
		if _, alreadyCreating := a.creatingIDs.LoadOrStore(id, struct{}{}); alreadyCreating {
			return "", "", fmt.Errorf("bad request: session creation already in progress")
		}
		defer a.creatingIDs.Delete(id)

		// Check active sessions.
		if existing, loaded := a.sessions.Load(id); loaded {
			mgr, ok := existing.(*SessionManager)
			if !ok {
				return "", "", fmt.Errorf("internal error: unexpected session type")
			}
			if mgr.user.UserID != user.UserID {
				return "", "", fmt.Errorf("bad request")
			}
			return id, "already_exists", nil
		}

		// Check persisted sessions.
		if a.store != nil {
			if sess, err := a.store.GetSession(ctx, id); err == nil && sess != nil {
				if sess.UserID != user.UserID {
					return "", "", fmt.Errorf("bad request")
				}
				return id, "already_exists", nil
			}
		}
	} else {
		id = uuid.Must(uuid.NewV7()).String()
	}

	model := selectModel(req.Model, "", a.getDefaultModelID(ctx))

	provider, modelCfg, err := a.resolveProvider(ctx, model)
	if err != nil {
		a.logger.Error("Failed to get LLM provider", "error", err)
		return "", "", ErrAgentNotConfigured
	}

	now := time.Now()

	resolved := a.resolveContexts(ctx, req.DAGContexts)
	var dagName string
	if len(resolved) > 0 {
		dagName = resolved[0].DAGName
	}

	mgr := NewSessionManager(a.buildSessionManagerConfig(id, user, sessionRuntimeConfig{
		modelID:         model,
		dagName:         dagName,
		safeMode:        req.SafeMode,
		soul:            a.loadSoulWithOverride(ctx, req.SoulID),
		webSearch:       a.loadWebSearch(ctx),
		thinkingEffort:  modelThinkingEffort(modelCfg),
		inputCostPer1M:  modelCfg.InputCostPer1M,
		outputCostPer1M: modelCfg.OutputCostPer1M,
	}))
	mgr.registry = &sessionRegistry{sessions: &a.sessions, parent: mgr}

	messageWithContext, err := a.prepareChatContent(ctx, id, req)
	if err != nil {
		a.logger.Error("Failed to prepare chat content", "error", err)
		return "", "", ErrFailedToProcessMessage
	}

	// Persist session before accepting the first message so that
	// the onMessage callback (store.AddMessage) can find the session.
	a.persistNewSession(ctx, id, user.UserID, dagName, model, now)
	a.sessions.Store(id, mgr)

	if err := mgr.AcceptUserMessage(ctx, provider, model, modelCfg.Model, messageWithContext); err != nil {
		a.logger.Error("Failed to accept user message", "error", err)
		a.sessions.Delete(id)
		return "", "", ErrFailedToProcessMessage
	}

	return id, "accepted", nil
}

// CreateEmptySession creates a persisted session without an initial user turn.
// This is used by chat bridges that need a conversation container before the
// next live user message, such as notification-seeded bot sessions.
func (a *API) CreateEmptySession(ctx context.Context, user UserIdentity, dagName string, safeMode bool) (string, error) {
	cfg, err := a.defaultSessionRuntime(ctx, dagName, safeMode)
	if err != nil {
		return "", err
	}

	id := uuid.Must(uuid.NewV7()).String()
	a.newManagedSession(ctx, id, user, cfg, time.Now())
	return id, nil
}

// GenerateAssistantMessage runs a one-shot assistant generation without mutating
// session state. When sessionID is provided, the current session's model, soul,
// DAG scope, and enabled skills are reused.
func (a *API) GenerateAssistantMessage(ctx context.Context, sessionID string, user UserIdentity, dagName, prompt string) (Message, error) {
	if strings.TrimSpace(prompt) == "" {
		return Message{}, ErrMessageRequired
	}

	var (
		runtimeCfg sessionRuntimeConfig
		provider   llm.Provider
		err        error
	)

	if sessionID != "" {
		mgr, ok := a.getOrReactivateSession(ctx, sessionID, user)
		if !ok {
			return Message{}, ErrSessionNotFound
		}
		runtimeCfg, err = a.runtimeConfigForSession(ctx, mgr, dagName)
		if err != nil {
			return Message{}, err
		}
		provider, _, err = a.resolveProvider(ctx, runtimeCfg.modelID)
		if err != nil {
			return Message{}, err
		}
	} else {
		runtimeCfg, err = a.defaultSessionRuntime(ctx, dagName, false)
		if err != nil {
			return Message{}, err
		}
		provider, _, err = a.resolveProvider(ctx, runtimeCfg.modelID)
		if err != nil {
			return Message{}, err
		}
	}

	systemPrompt := a.buildSystemPrompt(ctx, user.Role, runtimeCfg.dagName, runtimeCfg.soul)
	resp, err := a.runOneShotPrompt(ctx, provider, runtimeCfg.resolvedModel, systemPrompt, prompt)
	if err != nil {
		return Message{}, err
	}
	if len(resp.ToolCalls) > 0 {
		return Message{}, fmt.Errorf("unexpected tool call in one-shot assistant generation")
	}
	if strings.TrimSpace(resp.Content) == "" {
		return Message{}, fmt.Errorf("assistant returned empty content")
	}

	llmMsg := llm.Message{
		Role:    llm.RoleAssistant,
		Content: resp.Content,
	}
	msg := Message{
		Type:      MessageTypeAssistant,
		Content:   resp.Content,
		Usage:     &resp.Usage,
		CreatedAt: time.Now(),
		LLMData:   &llmMsg,
	}
	return msg, nil
}

// AppendExternalMessage appends a prebuilt message into a session and returns
// the stored message with assigned sequence metadata.
func (a *API) AppendExternalMessage(ctx context.Context, sessionID string, user UserIdentity, msg Message) (Message, error) {
	mgr, ok := a.getOrReactivateSession(ctx, sessionID, user)
	if !ok {
		return Message{}, ErrSessionNotFound
	}
	return mgr.RecordExternalMessage(ctx, msg)
}

// CompactSessionIfNeeded creates a continuation session with an LLM-generated
// handoff summary when the current session is approaching its context window.
func (a *API) CompactSessionIfNeeded(ctx context.Context, sessionID string, user UserIdentity) (string, bool, error) {
	mgr, ok := a.getOrReactivateSession(ctx, sessionID, user)
	if !ok {
		return "", false, ErrSessionNotFound
	}
	if mgr.IsWorking() || mgr.HasPendingPrompt() {
		return sessionID, false, nil
	}

	runtimeCfg, err := a.runtimeConfigForSession(ctx, mgr, "")
	if err != nil {
		return "", false, err
	}
	messages := mgr.GetMessages()
	if !shouldCompactMessages(messages, runtimeCfg.modelCfg.ContextWindow) {
		return sessionID, false, nil
	}

	provider, _, err := a.resolveProvider(ctx, runtimeCfg.modelID)
	if err != nil {
		return "", false, err
	}
	transcript := buildCompactionTranscript(messages)
	if transcript == "" {
		return sessionID, false, nil
	}
	resp, err := a.runOneShotPrompt(ctx, provider, runtimeCfg.resolvedModel, compactionSummaryPrompt, "<session_transcript>\n"+transcript+"\n</session_transcript>")
	if err != nil {
		return "", false, err
	}
	if strings.TrimSpace(resp.Content) == "" {
		return "", false, fmt.Errorf("compaction summary was empty")
	}

	newID := uuid.Must(uuid.NewV7()).String()
	newMgr := a.newManagedSession(ctx, newID, user, runtimeCfg, time.Now())
	summaryContent := sessionSummaryPrefix + strings.TrimSpace(resp.Content)
	if _, err := newMgr.RecordExternalMessage(ctx, Message{
		Type:      MessageTypeAssistant,
		Content:   summaryContent,
		Usage:     &resp.Usage,
		CreatedAt: time.Now(),
		LLMData: &llm.Message{
			Role:    llm.RoleAssistant,
			Content: summaryContent,
		},
	}); err != nil {
		a.sessions.Delete(newID)
		return "", false, err
	}

	mgr.mu.Lock()
	queuedChatMessages := append([]string(nil), mgr.queuedChatMessages...)
	flushingQueuedChat := mgr.flushingQueuedChat
	mgr.mu.Unlock()
	if len(queuedChatMessages) > 0 || flushingQueuedChat {
		newMgr.mu.Lock()
		newMgr.queuedChatMessages = append(newMgr.queuedChatMessages, queuedChatMessages...)
		newMgr.flushingQueuedChat = flushingQueuedChat
		newMgr.mu.Unlock()
	}
	if err := a.ensureSessionLoop(newMgr, provider, runtimeCfg); err != nil {
		a.sessions.Delete(newID)
		if a.store != nil {
			if delErr := a.store.DeleteSession(ctx, newID); delErr != nil && !errors.Is(delErr, ErrSessionNotFound) {
				a.logger.Warn("Failed to roll back compacted session after loop init error", "session_id", newID, "error", delErr)
			}
		}
		return "", false, err
	}

	_ = mgr.Cancel(ctx)
	a.sessions.Delete(sessionID)

	return newID, true, nil
}

// ChatQueueResult describes the outcome of a bot chat enqueue/flush request.
type ChatQueueResult struct {
	SessionID string
	Rotated   bool
	Queued    bool
	Started   bool
}

func (a *API) prepareSessionMessage(ctx context.Context, mgr *SessionManager, user UserIdentity, req ChatRequest) (llm.Provider, string, string, string, error) {
	provider, model, resolvedModel, err := a.prepareSessionRuntime(ctx, mgr, user, req)
	if err != nil {
		return nil, "", "", "", err
	}
	messageWithContext, err := a.prepareChatContent(ctx, mgr.ID(), req)
	if err != nil {
		return nil, "", "", "", err
	}
	return provider, model, resolvedModel, messageWithContext, nil
}

func (a *API) prepareSessionRuntime(ctx context.Context, mgr *SessionManager, user UserIdentity, req ChatRequest) (llm.Provider, string, string, error) {
	mgr.UpdateUserContext(user)

	if req.Message == "" {
		return nil, "", "", ErrMessageRequired
	}

	model := selectModel(req.Model, mgr.GetModel(), a.getDefaultModelID(ctx))
	provider, modelCfg, err := a.resolveProvider(ctx, model)
	if err != nil {
		a.logger.Error("Failed to get LLM provider", "error", err)
		return nil, "", "", ErrAgentNotConfigured
	}

	mgr.SetSafeMode(req.SafeMode)
	mgr.UpdatePricing(modelCfg.InputCostPer1M, modelCfg.OutputCostPer1M)
	mgr.UpdateThinkingEffort(modelThinkingEffort(modelCfg))
	mgr.UpdateLoopProvider(provider, modelCfg.Model)
	a.persistSessionModel(ctx, mgr, model)
	return provider, model, modelCfg.Model, nil
}

// EnqueueChatMessage accepts bot chat input for an existing session. Idle sessions
// start immediately, while working sessions merge the text into a queued
// safe-boundary interrupt turn.
func (a *API) EnqueueChatMessage(ctx context.Context, sessionID string, user UserIdentity, req ChatRequest) (ChatQueueResult, error) {
	mgr, ok := a.getOrReactivateSession(ctx, sessionID, user)
	if !ok {
		return ChatQueueResult{}, ErrSessionNotFound
	}

	provider, model, resolvedModel, err := a.prepareSessionRuntime(ctx, mgr, user, req)
	if err != nil {
		return ChatQueueResult{}, err
	}

	if mgr.IsWorking() || mgr.HasQueuedChatInput() {
		queued, err := mgr.EnqueueChatMessage(ctx, provider, model, resolvedModel, req.Message)
		if err != nil {
			a.logger.Error("Failed to enqueue chat message", "error", err)
			return ChatQueueResult{}, ErrFailedToProcessMessage
		}
		return ChatQueueResult{SessionID: sessionID, Queued: queued}, nil
	}

	targetSessionID, rotated, err := a.CompactSessionIfNeeded(ctx, sessionID, user)
	if err != nil {
		return ChatQueueResult{}, err
	}
	targetMgr, ok := a.getOrReactivateSession(ctx, targetSessionID, user)
	if !ok {
		return ChatQueueResult{}, ErrSessionNotFound
	}
	provider, model, resolvedModel, err = a.prepareSessionRuntime(ctx, targetMgr, user, req)
	if err != nil {
		return ChatQueueResult{}, err
	}
	messageWithContext, err := a.prepareChatContent(ctx, targetSessionID, req)
	if err != nil {
		a.logger.Error("Failed to prepare queued chat content", "error", err)
		return ChatQueueResult{}, ErrFailedToProcessMessage
	}
	queued, err := targetMgr.EnqueueChatMessage(ctx, provider, model, resolvedModel, messageWithContext)
	if err != nil {
		a.logger.Error("Failed to enqueue chat message", "error", err)
		return ChatQueueResult{}, ErrFailedToProcessMessage
	}
	return ChatQueueResult{SessionID: targetSessionID, Rotated: rotated, Queued: queued}, nil
}

// FlushQueuedChatMessage starts a previously queued bot follow-up turn when the
// session becomes idle. It compacts/rotates first if the session is near its
// context window.
func (a *API) FlushQueuedChatMessage(ctx context.Context, sessionID string, user UserIdentity) (ChatQueueResult, error) {
	mgr, ok := a.getOrReactivateSession(ctx, sessionID, user)
	if !ok {
		return ChatQueueResult{}, ErrSessionNotFound
	}
	if mgr.IsWorking() || mgr.HasPendingPrompt() {
		return ChatQueueResult{SessionID: sessionID}, nil
	}

	text, ok := mgr.BeginQueuedChatFlush()
	if !ok {
		return ChatQueueResult{SessionID: sessionID}, nil
	}

	targetSessionID := sessionID
	rotated := false

	targetSessionID, rotated, err := a.CompactSessionIfNeeded(ctx, sessionID, user)
	if err != nil {
		mgr.RestoreQueuedChatInput(text)
		return ChatQueueResult{}, err
	}

	targetMgr, ok := a.getOrReactivateSession(ctx, targetSessionID, user)
	if !ok {
		if rotated {
			mgr.RestoreQueuedChatInput(text)
		} else {
			mgr.RestoreQueuedChatInput(text)
		}
		return ChatQueueResult{}, ErrSessionNotFound
	}

	req := ChatRequest{
		Message:  text,
		SafeMode: targetMgr.safeMode,
	}
	provider, model, resolvedModel, err := a.prepareSessionRuntime(ctx, targetMgr, user, req)
	if err != nil {
		targetMgr.RestoreQueuedChatInput(text)
		return ChatQueueResult{}, err
	}
	messageWithContext, err := a.prepareChatContent(ctx, targetSessionID, req)
	if err != nil {
		targetMgr.RestoreQueuedChatInput(text)
		a.logger.Error("Failed to prepare queued chat content", "error", err)
		return ChatQueueResult{}, ErrFailedToProcessMessage
	}
	if err := targetMgr.AcceptUserMessage(ctx, provider, model, resolvedModel, messageWithContext); err != nil {
		targetMgr.RestoreQueuedChatInput(text)
		a.logger.Error("Failed to flush queued chat message", "error", err)
		return ChatQueueResult{}, ErrFailedToProcessMessage
	}
	targetMgr.CompleteQueuedChatFlush()
	return ChatQueueResult{SessionID: targetSessionID, Rotated: rotated, Started: true}, nil
}

// isValidUUID checks if a string is a valid UUID.
func isValidUUID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}

// ListSessions returns all sessions for the given user.
func (a *API) ListSessions(ctx context.Context, userID string) []SessionWithState {
	activeIDs := make(map[string]struct{})
	sessions := a.collectActiveSessions(userID, activeIDs)
	sessions = a.appendPersistedSessions(ctx, userID, activeIDs, sessions)

	if sessions == nil {
		sessions = []SessionWithState{}
	}
	return sessions
}

// ListSessionsPaginated returns a paginated list of sessions for the given user.
// Active sessions appear first, followed by persisted inactive sessions.
func (a *API) ListSessionsPaginated(ctx context.Context, userID string, page, perPage int) exec.PaginatedResult[SessionWithState] {
	pg := exec.NewPaginator(page, perPage)

	activeIDs := make(map[string]struct{})
	activeSessions := a.collectActiveSessions(userID, activeIDs)

	combined := make([]SessionWithState, 0, len(activeSessions))
	combined = append(combined, activeSessions...)

	if a.store != nil {
		persisted, err := a.store.ListSessions(ctx, userID)
		if err != nil {
			a.logger.Warn("Failed to list persisted sessions", "error", err)
		} else {
			for _, sess := range persisted {
				if _, exists := activeIDs[sess.ID]; exists {
					continue
				}
				if sess.ParentSessionID != "" {
					continue
				}
				combined = append(combined, SessionWithState{
					Session: *sess,
					Working: false,
				})
			}
		}
	}

	total := len(combined)
	start := min(pg.Offset(), total)
	end := min(pg.Offset()+pg.Limit(), total)
	pageItems := combined[start:end]

	// Load costs only for the visible page's inactive sessions.
	for i := range pageItems {
		if _, isActive := activeIDs[pageItems[i].Session.ID]; !isActive {
			pageItems[i].TotalCost = a.getStoredSessionCost(ctx, pageItems[i].Session.ID)
		}
	}

	return exec.NewPaginatedResult(pageItems, total, pg)
}

// GetSessionDetail returns session details including messages and state.
func (a *API) GetSessionDetail(ctx context.Context, sessionID, userID string) (*StreamResponse, error) {
	// Check active sessions first
	if mgr, ok := a.getActiveSession(sessionID, userID); ok {
		snapshot := mgr.Snapshot()
		return responseWithDelegates(
			snapshot.StreamResponse(),
			snapshot.Delegates,
		), nil
	}

	// Fall back to store for inactive sessions
	sess, messages, err := a.getStoredSession(ctx, sessionID, userID)
	if err != nil {
		return nil, err
	}

	delegates, err := a.getStoredDelegateSnapshots(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	totalCost := sumMessageCosts(messages) + a.sumSubSessionCosts(ctx, sessionID)

	return &StreamResponse{
		Messages: messages,
		Session:  sess,
		SessionState: &SessionState{
			SessionID: sessionID,
			Working:   false,
			TotalCost: totalCost,
		},
		Delegates: delegates,
	}, nil
}

func (a *API) getStoredDelegateSnapshots(ctx context.Context, sessionID string) ([]DelegateSnapshot, error) {
	if a.store == nil {
		return nil, ErrSessionNotFound
	}

	subSessions, err := a.store.ListSubSessions(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	delegates := make([]DelegateSnapshot, 0, len(subSessions))
	for _, sub := range subSessions {
		delegates = append(delegates, DelegateSnapshot{
			ID:     sub.ID,
			Task:   sub.DelegateTask,
			Status: DelegateStatusCompleted,
			Cost:   a.sumSessionMessageCosts(ctx, sub.ID),
		})
	}

	return delegates, nil
}

func responseWithDelegates(response StreamResponse, delegates []DelegateSnapshot) *StreamResponse {
	response.Delegates = delegates
	return &response
}

// SendMessage sends a message to an existing session.
func (a *API) SendMessage(ctx context.Context, sessionID string, user UserIdentity, req ChatRequest) error {
	mgr, ok := a.getOrReactivateSession(ctx, sessionID, user)
	if !ok {
		return ErrSessionNotFound
	}
	provider, model, resolvedModel, messageWithContext, err := a.prepareSessionMessage(ctx, mgr, user, req)
	if err != nil {
		if errors.Is(err, ErrMessageRequired) || errors.Is(err, ErrAgentNotConfigured) {
			return err
		}
		a.logger.Error("Failed to prepare chat content", "error", err)
		return ErrFailedToProcessMessage
	}
	if err := mgr.AcceptUserMessage(ctx, provider, model, resolvedModel, messageWithContext); err != nil {
		a.logger.Error("Failed to accept user message", "error", err)
		return ErrFailedToProcessMessage
	}

	return nil
}

// CancelSession cancels an active session.
func (a *API) CancelSession(ctx context.Context, sessionID, userID string) error {
	mgr, ok := a.getActiveSession(sessionID, userID)
	if !ok {
		return ErrSessionNotFound
	}

	if err := mgr.Cancel(ctx); err != nil {
		a.logger.Error("Failed to cancel session", "error", err)
		return ErrFailedToCancel
	}

	return nil
}

// SubmitUserResponse submits a user's response to an agent prompt.
func (a *API) SubmitUserResponse(_ context.Context, sessionID, userID string, resp UserPromptResponse) error {
	mgr, ok := a.getActiveSession(sessionID, userID)
	if !ok {
		return ErrSessionNotFound
	}

	if resp.PromptID == "" {
		return ErrPromptIDRequired
	}

	if !mgr.SubmitUserResponse(resp) {
		return ErrPromptExpired
	}

	return nil
}

// SubscribeSession subscribes to real-time updates for a session.
// Returns the initial snapshot and a next function that blocks until the next
// update is available. The bool return from next is false when the subscription ends.
// Returns ErrSessionNotFound if the session doesn't exist or the user doesn't match.
func (a *API) SubscribeSession(ctx context.Context, sessionID string, user UserIdentity) (StreamResponse, func() (StreamResponse, bool), error) {
	mgr, ok := a.getOrReactivateSession(ctx, sessionID, user)
	if !ok {
		return StreamResponse{}, nil, ErrSessionNotFound
	}
	snapshot, next := mgr.SubscribeWithSnapshot(ctx)
	return snapshot, next, nil
}

// EnabledMiddleware returns middleware that checks if agent is enabled.
func (a *API) EnabledMiddleware() func(http.Handler) http.Handler {
	return a.enabledMiddleware()
}

// HandleStream provides SSE streaming for session updates.
func (a *API) HandleStream(w http.ResponseWriter, r *http.Request) {
	a.handleStream(w, r)
}

// heartbeatInterval is the SSE heartbeat period to keep connections alive.
const heartbeatInterval = 15 * time.Second

// idleSessionTimeout is the duration after which idle sessions are cleaned up.
const idleSessionTimeout = 30 * time.Minute

// cleanupInterval is how often the cleanup goroutine runs.
// Stuck sessions become eligible for cancellation after stuckHeartbeatTimeout,
// but actual cancellation only happens on this sweep.
const cleanupInterval = 5 * time.Minute

// stuckHeartbeatTimeout is the maximum time without a heartbeat before
// a working session is considered stuck and cancelled.
const stuckHeartbeatTimeout = 3 * loopHeartbeatInterval

// StartCleanup begins periodic cleanup of idle sessions.
// It should be called once when the API is initialized and will
// stop when the context is cancelled.
func (a *API) StartCleanup(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(cleanupInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.cleanupIdleSessions()
			}
		}
	}()
}

// cleanupIdleSessions removes sessions that have been idle too long and are not working.
func (a *API) cleanupIdleSessions() {
	now := time.Now()
	cutoff := now.Add(-idleSessionTimeout)
	var toDelete []string

	a.sessions.Range(func(key, value any) bool {
		id, ok := key.(string)
		if !ok {
			return true
		}
		mgr, ok := value.(*SessionManager)
		if !ok {
			return true
		}
		// Skip delegate sub-sessions — their lifecycle is managed by the parent.
		sess := mgr.GetSession()
		if sess.ParentSessionID != "" {
			return true
		}
		if shouldCancelStuckSession(mgr, now) {
			if err := mgr.Cancel(context.Background()); err != nil {
				a.logger.Warn("Failed to cancel stuck session", "session_id", id, "error", err)
			} else {
				a.logger.Warn("Cancelled stuck session", "session_id", id)
			}
		}
		// Cancelled sessions remain in the map until the next cleanup cycle
		// so they can still be viewed or reactivated by the user.
		if !mgr.IsWorking() && mgr.LastActivity().Before(cutoff) {
			toDelete = append(toDelete, id)
		}
		return true
	})

	for _, id := range toDelete {
		if val, ok := a.sessions.Load(id); ok {
			if mgr, ok := val.(*SessionManager); ok {
				_ = mgr.Cancel(context.Background())
			}
		}
		a.sessions.Delete(id)
		a.logger.Debug("Cleaned up idle session", "session_id", id)
	}
}

func shouldCancelStuckSession(mgr *SessionManager, now time.Time) bool {
	if mgr == nil || !mgr.IsWorking() {
		return false
	}
	if mgr.HasPendingPrompt() {
		return false
	}
	lastHB := mgr.LastHeartbeat()
	return !lastHB.IsZero() && now.Sub(lastHB) > stuckHeartbeatTimeout
}
