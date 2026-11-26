// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/service/projectaccess"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"
)

// projectParam is the query parameter that names the project of a request.
const projectParam = "project"

// While projects are on, a request reaches the API of the project it names:
//
//	request ──▶ dispatch: names a project?
//	              no  ──▶ default project's API; an operation that takes
//	                      project checks the caller's role there
//	              yes ──▶ that project's API, or 404 when the caller
//	                      cannot see the project
//
// Each project's API holds that project's stores, so a handler reads and
// writes only the project the request addresses.

type projectScopeKey struct{}

// withProjectScope returns ctx carrying the project a request addresses and
// the caller's role in it.
func withProjectScope(ctx context.Context, access projectaccess.Access) context.Context {
	return context.WithValue(ctx, projectScopeKey{}, access)
}

// projectScopeFrom returns the project a request addresses, when it names
// one or its operation takes project.
func projectScopeFrom(ctx context.Context) (projectaccess.Access, bool) {
	access, ok := ctx.Value(projectScopeKey{}).(projectaccess.Access)
	return access, ok
}

// WithProjectAPIs sets the APIs that serve the DAGs and runs of each
// project while projects are on. lookup returns the API of a registered
// project, the default one included, and false while the project's stores
// are not open, as while the project is being deleted.
func WithProjectAPIs(lookup func(project.Project) (*API, bool)) APIOption {
	return func(a *API) {
		a.projectAPIs = lookup
	}
}

// projectOperations returns the IDs of the operations that take project.
func projectOperations(swagger *openapi3.T) map[string]bool {
	ops := make(map[string]bool)
	for _, item := range swagger.Paths.Map() {
		for _, op := range item.Operations() {
			for _, param := range op.Parameters {
				if param.Value != nil && param.Value.In == openapi3.ParameterInQuery && param.Value.Name == projectParam {
					ops[op.OperationID] = true
				}
			}
		}
	}
	return ops
}

// projectRoutes returns the handler of the API's routes, built once by
// build.
func (a *API) projectRoutes(build func(*API) http.Handler) http.Handler {
	a.routesOnce.Do(func() {
		a.routes = build(a)
	})
	return a.routes
}

// dispatchProjects serves a request that names a project with that
// project's API, and any other request with the API of the default
// project. Each API serves the operations with middlewares and options.
func (a *API) dispatchProjects(
	swagger *openapi3.T,
	middlewares []api.StrictMiddlewareFunc,
	options api.StrictHTTPServerOptions,
) http.Handler {
	ops := projectOperations(swagger)
	build := func(target *API) http.Handler {
		scoped := append(slices.Clone(middlewares), target.projectScopeMiddleware(ops))
		return api.Handler(api.NewStrictHandlerWithOptions(target, scoped, options))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		values, named := r.URL.Query()[projectParam]
		if !named {
			a.projectRoutes(build).ServeHTTP(w, r)
			return
		}
		name := ""
		if len(values) > 0 {
			name = values[0]
		}
		access, err := a.projectAccessFor(r.Context(), name)
		if err != nil {
			a.handleError(w, r, err)
			return
		}
		target, ok := a.projectAPIs(access.Project)
		if !ok {
			a.handleError(w, r, projectNotFound(name))
			return
		}
		target.projectRoutes(build).ServeHTTP(w, r.WithContext(withProjectScope(r.Context(), access)))
	})
}

// projectScopeMiddleware gives an operation that takes project the caller's
// access to the default project when the request names none, and refuses
// project on any other operation.
func (a *API) projectScopeMiddleware(ops map[string]bool) api.StrictMiddlewareFunc {
	return func(next api.StrictHandlerFunc, operationID string) api.StrictHandlerFunc {
		if !ops[operationID] {
			return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
				if _, named := projectScopeFrom(ctx); named {
					return nil, &Error{
						HTTPStatus: http.StatusBadRequest,
						Code:       api.ErrorCodeBadRequest,
						Message:    fmt.Sprintf("%s does not take the %s parameter", operationID, projectParam),
					}
				}
				return next(ctx, w, r, request)
			}
		}
		return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
			if _, ok := projectScopeFrom(ctx); !ok {
				access, err := a.projectAccessFor(ctx, project.DefaultName)
				if err != nil {
					return nil, err
				}
				ctx = withProjectScope(ctx, access)
			}
			response, err := next(ctx, w, r, request)
			return response, a.dagNotFoundInProject(ctx, chi.URLParam(r, "fileName"), err)
		}
	}
}

// projectAccessFor returns the caller's access to the project called name.
func (a *API) projectAccessFor(ctx context.Context, name string) (projectaccess.Access, error) {
	user, err := a.projectCaller(ctx)
	if err != nil {
		return projectaccess.Access{}, err
	}
	access, err := a.projectAccess.Get(ctx, user, name)
	if errors.Is(err, project.ErrNotFound) {
		return projectaccess.Access{}, projectNotFound(name)
	}
	return access, err
}

// projectCaller returns the user whose projects a request reaches: the
// caller when roles apply to it, and otherwise an admin.
func (a *API) projectCaller(ctx context.Context) (*auth.User, error) {
	if !a.rolesApply(ctx) {
		return &auth.User{Role: auth.RoleAdmin}, nil
	}
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, errAuthRequired
	}
	return user, nil
}

func projectNotFound(name string) *Error {
	return &Error{
		HTTPStatus: http.StatusNotFound,
		Code:       api.ErrorCodeNotFound,
		Message:    fmt.Sprintf("project %q not found", name),
	}
}

// dagNotFoundInProject returns err, or, when err answers 404 for the DAG
// called name and the addressed project has no such DAG, an error that
// names the projects the caller can see that have one.
func (a *API) dagNotFoundInProject(ctx context.Context, name string, err error) error {
	if err == nil || name == "" {
		return err
	}
	if _, _, status := a.resolveError(err); status != http.StatusNotFound {
		return err
	}
	scope, ok := projectScopeFrom(ctx)
	if !ok || hasDAG(ctx, a, name) {
		return err
	}
	user, callerErr := a.projectCaller(ctx)
	if callerErr != nil {
		return err
	}
	visible, listErr := a.projectAccess.List(ctx, user)
	if listErr != nil {
		return err
	}
	var others []string
	for _, access := range visible {
		if access.Project.ID == scope.Project.ID {
			continue
		}
		if other, ok := a.projectAPIs(access.Project); ok && hasDAG(ctx, other, name) {
			others = append(others, access.Project.Name)
		}
	}
	message := fmt.Sprintf("DAG %s not found in project %s", name, scope.Project.Name)
	if len(others) > 0 {
		message += "; projects with a DAG of that name: " + strings.Join(others, ", ")
	}
	return &Error{HTTPStatus: http.StatusNotFound, Code: api.ErrorCodeNotFound, Message: message}
}

// hasDAG reports whether the project of a has a DAG called name. A DAG that
// cannot be read counts, since its file exists.
func hasDAG(ctx context.Context, a *API, name string) bool {
	_, err := a.dagRepository.GetMetadata(ctx, name)
	return !errors.Is(err, os.ErrNotExist) && !errors.Is(err, persis.ErrDAGNotFound)
}

// refuseQueuedRun refuses to queue a run outside the default project, whose
// queues are the only ones the scheduler reads.
func (a *API) refuseQueuedRun() error {
	return a.refuseInProject(project.ErrQueuedRun)
}

// refuseWorkerRun refuses to send a run outside the default project to a
// worker, since the coordinator records it in the default project.
func (a *API) refuseWorkerRun() error {
	return a.refuseInProject(project.ErrWorkerRun)
}

func (a *API) refuseInProject(reason error) error {
	if err := project.RefuseOtherProject(a.dagRunMgr.Project(), reason); err != nil {
		return &Error{HTTPStatus: http.StatusBadRequest, Code: api.ErrorCodeBadRequest, Message: err.Error()}
	}
	return nil
}
