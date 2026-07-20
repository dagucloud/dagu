# Spec: Scalar Input Schema

## Status

Not implemented.

Schema-backed DAG parameters are the reference conformance surface for this
shared contract. Other owning specs test only their integration with it.

## Scope

This spec defines the reusable scalar-field schema used by typed Dagu inputs.
An owning spec opts into this contract and defines the object that contains the
fields, how values are supplied, and how validated values are consumed.

This spec defines:

- the supported scalar types
- field metadata and constraints
- type inference from `enum` and `oneOf`
- defaults and validation order
- string-to-scalar coercion for CLI `key=value` inputs

This spec does not define:

- DAG parameter lookup or `${params.*}` references
- a root object schema, `required`, or `additionalProperties`
- nested objects or arrays
- input persistence, step outputs, or run lifecycle behavior
- UI rendering

Schema-backed parameter properties opt into this spec. Inline rich parameter
declarations can expose equivalent snake-case fields under their owning
parameter contract.

## Goal

DAG parameters, human-task forms, and later typed-input surfaces can share one
small scalar contract instead of defining subtly different type and validation
rules.

## Terms

A scalar value is a string, integer, number, or boolean. It is never `null`, an
object, or an array.

An integer is a whole number in the signed 64-bit range.

A number is a finite IEEE 754 binary64 value. Every integer is also valid where
the declared type is `number`.

Scalar equality compares strings and booleans exactly and compares numeric
values mathematically. For numeric equality, `1` and `1.0` are equal.

ASCII whitespace means space, horizontal tab, carriage return, or line feed.

## Behavior

### Field Shape

A scalar-field schema is an object supporting only these fields:

| Field | Required | Meaning |
| --- | --- | --- |
| `type` | One of `type`, `enum`, `oneOf` | `string`, `integer`, `number`, or `boolean`. |
| `title` | No | String display metadata. |
| `description` | No | String help metadata. |
| `default` | No | Non-null scalar default. |
| `enum` | One of `type`, `enum`, `oneOf` | Non-empty list of unique scalar values. |
| `oneOf` | One of `type`, `enum`, `oneOf` | Non-empty list of labeled scalar choices. |
| `minimum` | No | Inclusive numeric lower bound. |
| `maximum` | No | Inclusive numeric upper bound. |
| `minLength` | No | Inclusive string-length lower bound. |
| `maxLength` | No | Inclusive string-length upper bound. |
| `pattern` | No | Go RE2 regular expression matched against the string. |

Rules:

- A field not listed in the table is invalid.
- At least one of `type`, `enum`, or `oneOf` is required.
- `title` and `description` do not affect validation.
- `default`, `enum`, and `oneOf[].const` must be scalar values.
- `enum` values must be unique under scalar equality.
- `oneOf` choice constants must be unique under scalar equality.

### Type Resolution

Rules:

- An explicit `type` determines the field type.
- Without an explicit `type`, `enum` values or `oneOf[].const` values determine
  the type.
- All inferred values must resolve to one type under the following rules.
- A set containing only whole numbers infers `integer`.
- A set containing whole and non-whole numbers infers `number`.
- Strings, numbers, and booleans cannot be mixed during inference.
- When an explicit type, `enum`, and `oneOf` are combined, every value must
  validate against the explicit type and all constraints apply.

### Choices

Each `oneOf` item supports only these fields:

| Field | Required | Meaning |
| --- | --- | --- |
| `const` | Yes | Non-null scalar choice value. |
| `type` | No | Must equal the resolved field type. An integer is also valid for `number`. |
| `title` | No | String choice label. |
| `description` | No | String choice help text. |

The submitted value must equal exactly one `const`. Choice metadata does not
affect validation.

### Constraints

Rules:

- `minimum` and `maximum` are valid only for `integer` and `number` fields.
- `minimum` must be less than or equal to `maximum` when both are present.
- `minLength`, `maxLength`, and `pattern` are valid only for `string` fields.
- Length counts Unicode code points, not UTF-8 bytes.
- `minLength` and `maxLength` must be non-negative integers.
- `minLength` must be less than or equal to `maxLength` when both are present.
- `pattern` uses Go RE2 syntax and succeeds when any substring matches. Authors
  use `^` and `$` when the whole value must match.
- An invalid regular expression is a schema error.

### Defaults And Validation

An owning input surface validates values in this order:

1. Parse its input transport.
2. Perform string coercion if that surface opts into it.
3. Apply defaults to omitted fields.
4. Enforce required-field rules owned by that surface.
5. Validate type, choice, enum, range, length, and pattern constraints.

A default must satisfy the complete scalar-field schema when the DAG is
validated. A supplied value takes precedence over a default.

### String Input Coercion

An owning spec can opt a `key=value` input into these coercion rules:

- `string`: preserve the value exactly.
- `integer`: trim surrounding ASCII whitespace, require syntax
  `-?(0|[1-9][0-9]*)`, and parse within the signed 64-bit range.
- `number`: trim surrounding ASCII whitespace, require syntax
  `-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?`, and parse to a finite
  binary64 value.
- `boolean`: trim surrounding ASCII whitespace and accept only
  ASCII-case-insensitive `true` or `false`.

String coercion never converts an empty string to `null` or treats it as an
omitted value. Typed JSON input is validated as supplied and does not use these
string-coercion rules.

## Errors

Validation must reject:

- a non-object scalar-field schema
- an unsupported field
- a missing or unsupported type definition
- mixed incompatible inferred types
- null, object, or array defaults and choice values
- duplicate enum values or choice constants
- a default that violates its field schema
- a constraint used with an incompatible type
- an invalid or contradictory bound
- an invalid regular expression

A schema error must identify the owning field and invalid keyword. A coercion
error must identify the owning field and expected scalar type.

## Examples

Scalar fields hosted by schema-backed DAG parameters:

```yaml
params:
  type: object
  properties:
    environment:
      type: string
      enum: [staging, production]
    replicas:
      type: integer
      minimum: 1
      maximum: 20
      default: 2
    notify:
      type: boolean
      default: true
  required: [environment]

steps:
  - id: show
    run: |
      printf '%s,%s,%s\n' \
        '${params.environment}' '${params.replicas}' '${params.notify}'
```

Labeled choices with inferred string type:

```yaml
oneOf:
  - const: staging
    title: Staging
  - const: production
    title: Production
```
