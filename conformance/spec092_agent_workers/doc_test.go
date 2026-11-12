// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec092_agent_workers holds black-box conformance tests for Spec 092,
// "Agent Workers". The tests run the Dagu binary of DAGU_BIN on static fixtures
// in testdata. They cover the routing of a run that needs a CLI provider, the
// reserved labels, the checks of `dagu worker --agent` at start, one run and
// the exit of an agent worker, the worker pools with test certificates, the
// step scope of secrets, the untrusted steps and the key injection of the
// egress proxy. A test that changes to the restricted user needs Linux, the user
// ID 0 and the flag no-new-privileges. It skips on another host.
package spec092_agent_workers_test
