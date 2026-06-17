# Spec Implementation Guide

This guide explains how to implement the specs. It is not a numbered spec and does not define workflow language behavior; the numbered specs are the source of truth.

## Contract

**Rules:**

- When existing Dagu behavior conflicts with a spec, the spec wins.
- When existing Dagu behavior is not defined by a spec, do not preserve it for compatibility.
- Add black-box tests for every behavior defined by a v3 spec under `../conformance/<spec_slug>`.
- Do not add fallback behavior for old YAML fields, old executor behavior, old value syntax, or old lifecycle behavior unless a spec requires it.
- Unspecified behavior must fail clearly or remain unsupported.
- Specs can land ahead of their implementation. Each implementation slice must document the spec behavior it covers and must not imply conformance to unimplemented behavior.
- Delete obsolete tests when they are replaced by new tests that cover the same behavior. Do not preserve old tests that verify removed behavior.
- Delete obsolete code when specs replace its behavior. Do not preserve old code that implements removed behavior.

## Code

**Implementation notes:**

- Prefer simple code with explicit boundaries.
- Delete or replace old code when keeping it makes the implementation harder to understand or verify.
- Reuse old code only when it directly implements the contract without hidden compatibility behavior.
- Avoid broad adapters that translate old behavior into behavior.
- Keep parsers strict.
- Keep errors deterministic enough that black-box tests can assert the failure class and invalid field path.
- Avoid global mutable registries unless the spec requires runtime extension.
- Keep data-plane code independent from control-plane packages.

**Deep module principles:**

- Prefer modules with small public interfaces and substantial internal implementation.
- Hide parsing, normalization, state transitions, retries, cleanup, and executor details behind narrow contracts.
- Make callers depend on what a module guarantees, not how the module does the work.
- Avoid shallow modules that expose many knobs while doing little work internally.
- Do not leak old Dagu compatibility concerns through module interfaces.
- Keep each public type or function useful enough that callers do not need to understand its internals.
- Move complexity behind a boundary only when the boundary makes the rest of the system simpler.

## Coverage

**Coverage expectations:**

- Target complete coverage of every behavior defined by the numbered specs.
- Unit tests should cover nearly all implementation paths.
- Exercise successful behavior, invalid input, runtime failure, timeout, abort, cleanup, and lifecycle effects when the relevant spec defines them.
- Cover public behavior from the outside with black-box tests.
- Add or update black-box tests in the same change that claims spec conformance.
- Use lower-level tests for parsing details, rare branches, and failure paths that are impractical to trigger through a full run.
- Limit untested paths to cases that are genuinely hard to trigger.
- If a behavior cannot be tested directly, document the exception, the reason, and the smallest affected surface.
- Do not count old compatibility behavior toward coverage.

## Spec Changes

**Change rules:**

- Change the spec before changing behavior.
- If implementation work exposes an ambiguous requirement, update the relevant spec instead of guessing.
- If implementation work requires a tradeoff or exposes something maintainers should know, ask for a decision or update the relevant spec doc.
- If implementation work finds behavior that is needed but not specified, do not silently leave it unimplemented; ask for a decision or update the relevant spec doc.
- If behavior is intentionally unsupported, make that decision explicit in the relevant spec doc.
- Keep examples in specs runnable as black-box test inputs.
- Keep specified behavior concrete enough to prove conformance.

## Robustness

**Design rules:**

- Prefer rejection over silent coercion.
- Prefer explicit fields over inferred behavior.
- Prefer deterministic output over convenience.
- Prefer small, typed contracts over generic maps.
- Prefer whole-value references before adding selector or query languages.
- Do not parse logs as data.
- Do not expose implementation details as public behavior.
