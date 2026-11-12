// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec093_ai_endpoints holds black-box conformance tests for Spec 093,
// "AI Endpoints": the endpoint routes and the store of the keys, the block ai of
// the configuration, the models list and the model check. The tests run the Dagu
// binary of DAGU_BIN on static fixtures in testdata. A test that needs a server
// starts `dagu server` on a free port of the loopback address. The tests need no
// external service. The suites of Spec 093, Spec 094 and Spec 095 share their fake
// claude CLI and their helpers in conformance/aitest.
//
// A test of an agent worker needs the user ID 0, because the worker changes to
// the restricted user (Spec 092, "Start"). It skips on another user. Run it in a
// root container with the flag no-new-privileges.
package spec093_ai_endpoints_test
