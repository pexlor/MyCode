# Permission System Design

## Goal

Add a reusable permission policy package and enforce it before the agent executes any tool. The change must preserve the current agent construction API and must not add shell or file-writing tools.

## Scope

The permission system classifies a tool call as one of three levels:

- `Allow`: execute without prompting.
- `Ask`: request a decision from an injected approver before execution.
- `Deny`: never execute, regardless of the approver.

Rules are keyed by exact tool name. Unknown tools default to `Ask`, so registering a new tool cannot silently bypass permission checks.

## Permission Package

The `internal/permission` package exposes:

- A `Level` type with `Allow`, `Ask`, and `Deny` values.
- A `Request` value containing the tool call ID, tool name, and arguments.
- An `Approver` function type that accepts a context and request and returns an approval decision or error.
- A `Checker` that owns an immutable copy of the configured tool-name rules.
- A constructor for creating a checker from a rule map.
- A check method that returns the configured level or `Ask` for an unknown tool.

The checker validates levels supplied at construction time. Callers cannot mutate active policy by changing the original map after construction.

## Agent Integration

The agent gains optional permission configuration while retaining the existing `NewAgent` signature. Configuration methods set a checker and approver after construction. If no checker is supplied, the agent uses a safe default checker, meaning every tool requires approval. If approval is required but no approver is configured, execution is denied.

For every tool call, the agent performs permission evaluation before looking up or executing the registered tool:

1. `Allow`: execute normally.
2. `Deny`: return an error tool result without invoking the tool or approver.
3. `Ask`: emit a permission-request event, invoke the approver, then emit a permission-decision event. Execute only when approved.

An approver error produces an error tool result and is reported in the decision event. Context cancellation propagates through the approver and prevents tool execution.

Permission-denied results remain ordinary tool results so the LLM can observe the refusal and continue or finish its response. A denied call does not crash the whole agent loop.

## Events

Two agent events support future REPL and UI integrations:

- `PermissionRequestEvent`: includes the tool call ID, tool name, and arguments.
- `PermissionDecisionEvent`: includes the tool call ID, tool name, whether access was granted, and an optional error.

The request event is emitted immediately before calling the approver. The decision event is emitted immediately after it returns.

## Error Handling

- Invalid permission levels are rejected when constructing a checker.
- Missing tool call IDs or names retain the existing validation behavior.
- An absent approver for an `Ask` decision fails closed.
- An approver error fails closed and is preserved in the tool-result message.
- A `Deny` rule cannot be overridden by the approver.

## Compatibility

Existing callers of `NewAgent` continue to compile. Because the safe default requires approval and the current application does not yet configure an approver, the default application wiring will explicitly allow the existing read-only `ReadFile` tool. This preserves current behavior while ensuring future tools default to confirmation.

## Tests

Permission package tests cover:

- Explicit allow, ask, and deny rules.
- Unknown tools defaulting to ask.
- Invalid levels being rejected.
- Defensive copying of the rule map.

Agent tests cover:

- Allowed tools execute without calling the approver.
- Denied tools never execute and never call the approver.
- Asked tools execute after approval.
- Asked tools do not execute after rejection.
- Missing approvers fail closed.
- Approver errors fail closed.
- Permission request and decision events contain the expected metadata.

All new behavior is implemented test-first, followed by the package test suite and the full repository test suite.
