# Commit message format

The subject line has the form `<type>(<scope>): <summary>`.

- The subject line has 72 characters at most.
- The summary starts with a lower case verb in the imperative, for example `fix`, `add` or `remove`.
- The summary has no full stop at the end.

## Types

| Type | Use it for |
| --- | --- |
| `fix` | a change that corrects a defect |
| `feat` | a change that adds behavior |
| `test` | a change to the tests only |
| `docs` | a change to the documentation only |
| `chore` | a change to the build or the tools |

## Scope

The scope is the name of the module that the change touches, for example `calc`. Use one scope. Leave out the parentheses when no module fits.

## Example

`fix(calc): return the sum in add`
