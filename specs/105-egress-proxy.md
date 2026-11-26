# Spec 105: Egress Proxy

## Status

Partially implemented.

`dagu egress-proxy serve` forwards a request only to a destination of `--allow` and refuses the
others with the status 403. With `--upstream` and its key options, it sends a request in origin form
to the upstream with the real key. The options `--allow-with-credential`, `--proxy-credential-env`
and `--proxy-credential-file` are not built yet.

## Scope

This spec defines `dagu egress-proxy serve`:

- which destinations it allows
- the upstream route, which adds the real key of a model API
- destinations that only a client with the proxy credential can reach
- its log lines and its errors

This spec does not define:

- the network of a deployment and the environment of an agent worker. See
  [Spec 092](092-agent-workers.md#deployment-requirements).
- the AI gateway, which carries the model traffic of a `claude` step without the proxy. See
  Spec 094.

## Goal

An agent worker reaches the outside only through this proxy and the coordinator. The proxy limits
where requests go, keeps the destinations of trusted steps closed to the agent, and can add a model
API key that the agent never sees.

```text
  request from a client
    |
    +-- origin form: POST /v1/messages
    |     |
    |     +-- no --upstream ---------------------------> 400
    |     +-- --upstream ------------------------------> remove the client credential,
    |                                                    add the key, forward to the upstream
    |
    +-- absolute form (GET http://host/...) or CONNECT host:port
          |
          +-- destination in --allow ------------------> forward
          |
          +-- destination in --allow-with-credential
          |     +-- valid Proxy-Authorization ---------> remove the header, forward
          |     +-- missing or wrong ------------------> 407; nothing is dialed
          |
          +-- any other destination -------------------> 403
```

## Destinations

`dagu egress-proxy serve` forwards the requests of a client to the destinations of `--allow` and
refuses the others with the status 403.

| Topic | Rule |
| --- | --- |
| Entry | A host name or an address, with an optional port. |
| IPv6 address with a port | In brackets, such as `[2001:db8::1]:443`. |
| Name with letters outside ASCII | Matches in Unicode and in its IDNA ASCII form, such as `bücher.example` and `xn--bcher-kva.example`. A name that has no IDNA ASCII form is refused at start. |
| Wildcard entry | A name that only a wildcard entry allows, such as `*.github.io`, reaches public addresses only. So does an address that only a wildcard such as `*.0.0.1` matches. |
| Exact entry | An entry that names the host or the address reaches any address. |
| Public address | A special-purpose address, such as `198.18.0.1`, is not public. A NAT64 or 6to4 address is public only when the IPv4 address in it is. |
| Idle `CONNECT` tunnel | Ends after 10 minutes in which no byte moves. |
| Idle kept connection | Ends after 2 minutes with no request. |
| Protocol upgrade, such as WebSocket | Not forwarded, so no connection escapes these limits. The request goes without the upgrade, and a destination that switches protocols anyway gives the status 502. |

## Key injection

These options add a route to one upstream, with the real key of a model API. The route serves a CLI
provider other than `claude`. A `claude` step names an endpoint instead (Spec 094).

| Option | Rule |
| --- | --- |
| `--upstream <url>` | The scheme is `http` or `https`. No query and no fragment. The host and port of the URL MUST match an entry of `--allow` or of `--allow-with-credential`. |
| `--upstream-auth <form>` | `x-api-key` (default), `bearer` or `anthropic-oauth`. |
| `--upstream-key-env <name>` | The variable that holds the key. |
| `--upstream-key-file <path>` | The file that holds the key. Dagu removes the last line end. The file has at most 16384 bytes. |

With `--upstream`, give exactly one of `--upstream-key-env` and `--upstream-key-file`. Without
`--upstream`, the three other options are errors. A variable that does not exist, an empty value, a
file that the proxy cannot read, or an empty file stops the proxy at start. The error names the
option and the variable or the path, and never holds the key.

A request in origin form, such as `POST /v1/messages`, goes to the upstream. The proxy:

1. joins the path of the upstream URL and the path of the request, and keeps the query
2. removes the client headers `x-api-key`, `authorization` and `proxy-authorization`
3. sets `x-api-key: <key>`, or `authorization: Bearer <key>` with the forms `bearer` and
   `anthropic-oauth`
4. with the form `anthropic-oauth`, adds `oauth-2025-04-20` to the list of `anthropic-beta` when the
   list does not have it, and keeps the betas of the client
5. sends the request under the same rules as the other destinations

The form `anthropic-oauth` is for an OAuth token of a subscription. Claude Code sends the beta
`oauth-2025-04-20` with such a token. A client with a placeholder API key does not send it.

A request in origin form with the method `TRACE` or `TRACK` gets the status 405 and does not reach
the upstream, because the answer would echo the key to the client.

A request in absolute form and a `CONNECT` request keep their rules. A destination that `--allow`
does not list gets the status 403. Without `--upstream`, a request in origin form gets the status
400.

The proxy never writes the key in a log, an error or a response, and the key is never an argument of
a command.

The proxy writes one line for each request to the upstream. The line has the method, the host and
the port, the client credential header and the form. For the client credential and for the key, it
has the first 12 hexadecimal digits of the SHA-256, not the value. The line has no path, because a
path can hold a secret.

```text
egress proxy: allow POST api.anthropic.com:443 (upstream): client credential x-api-key sha256:4097889236a2 replaced by the key sha256:0123456789ab (anthropic-oauth)
```

`4097889236a2` is the start of the SHA-256 of `placeholder`.

## Destinations with a proxy credential

These options open a destination only to a client that sends the proxy credential. A trusted step,
such as a clone or a push, gets the credential as a step secret
([Spec 103](103-untrusted-steps.md#step-secrets)). The agent does not get it.

| Option | Rule |
| --- | --- |
| `--allow-with-credential <host[:port]>` | A destination, in the form of `--allow`. Repeat the option for each destination. |
| `--proxy-credential-env <name>` | The variable that holds the proxy credential. |
| `--proxy-credential-file <path>` | The file that holds the proxy credential. Dagu removes the last line end. |

These cases stop the proxy at start. Each error names the option and never holds the credential.

- `--allow-with-credential` without exactly one of `--proxy-credential-env` and
  `--proxy-credential-file`, or one of these two options without `--allow-with-credential`
- a credential shorter than 16 characters, or with a control character
- a destination that can match an entry of `--allow` and an entry of `--allow-with-credential`,
  because the two entries contradict each other
- a wildcard entry of `--allow` together with `--allow-with-credential`. A client that controls the
  DNS of a name below the wildcard could point the name at a destination of
  `--allow-with-credential` and reach it with no credential.

A request in absolute form or a `CONNECT` request to such a destination MUST have one header
`Proxy-Authorization: Basic <base64 of user:credential>`. The user name can be any name. A request
without the credential gets the status 407 and the header
`Proxy-Authenticate: Basic realm="dagu egress proxy"`, and the proxy dials nothing for it. The proxy
removes the header before it sends a request in absolute form to the destination. A log line tells
the result, without the credential:

```text
egress proxy: deny CONNECT github.com:443: no valid proxy credential
egress proxy: allow CONNECT github.com:443 (proxy credential)
```

The upstream can be a destination of `--allow-with-credential`. The upstream route needs no
credential, because the proxy itself opens the connection. A client cannot open a tunnel to the
same host without the credential, so it cannot send a key of its own there.

A trusted step gives the credential to git in the proxy URL, in a step variable. The token and the
credential are never in an argument:

```yaml
  - id: clone
    untrusted: false
    secrets: [GIT_TOKEN, PROXY_CREDENTIAL]
    env:
      - https_proxy: http://git:${PROXY_CREDENTIAL}@egress-proxy:8081
      - GIT_HTTP_PROXY_AUTHMETHOD: basic
    run: |
      git -c credential.helper='!f() { echo username=x-access-token; echo "password=$GIT_TOKEN"; }; f' clone "$REPO" repo
```

## Errors

These cases stop the proxy at start with exit code 1:

- an invalid option, or an invalid combination of options, as listed above
- an upstream key variable that does not exist or is empty, or an upstream key file that the proxy
  cannot read or that is empty

These cases give an HTTP status:

| Case | Status |
| --- | --- |
| A destination that neither `--allow` nor `--allow-with-credential` lists | 403 |
| A destination of `--allow-with-credential` without a valid `Proxy-Authorization` header | 407 |
| A request in origin form without `--upstream` | 400 |
| A request in origin form with the method `TRACE` or `TRACK` | 405 |
| A destination that switches protocols after the proxy removed the upgrade | 502 |

## Examples

A proxy for runs of the provider `claude`. The model traffic goes through the relay, so the proxy
has no upstream. It opens GitHub only to a client that sends the proxy credential: the steps clone
and push. The agent reaches no destination:

```text
dagu egress-proxy serve --listen 0.0.0.0:8081 \
  --allow-with-credential github.com:443 \
  --proxy-credential-file /run/secrets/proxy-credential
```

A request to `http://example.com/` through this proxy gets the status 403.

A proxy with key injection, for a CLI provider other than `claude` that uses the OpenAI API:

```text
dagu egress-proxy serve --listen 0.0.0.0:8081 \
  --allow api.openai.com:443 --allow git-server:8080 \
  --upstream https://api.openai.com/v1 --upstream-auth bearer \
  --upstream-key-file /run/secrets/openai-api-key
```

The agent sends `POST http://egress-proxy:8081/chat/completions` with a placeholder key. The proxy
sends `POST https://api.openai.com/v1/chat/completions` with the real key.

## Limits

- An agent can send data to a destination that the proxy allows.
- A destination of `--allow` is open to the agent. Put a destination that only trusted steps need in
  `--allow-with-credential`.
- The proxy sees the name of a `CONNECT`, not the name inside TLS. A destination of `--allow` whose
  servers also serve a destination of `--allow-with-credential`, such as two hosts behind one CDN,
  opens that destination to the agent.
- The proxy sends each path of a request in origin form to the upstream, with the key, so the agent
  can call each endpoint of the upstream that the key opens. The upstream route asks for no
  credential: each client that reaches the address of `--listen` can use the key. The network of
  the deployment limits who reaches the proxy.

## Open items

Each item has a default. The default applies until the owner decides.

| Item | Default |
| --- | --- |
| The paths that the upstream route allows. | Each path. A list of allowed paths is a later item. |

## Conformance

Black-box tests go in `conformance/spec105_egress_proxy/`. No test is there yet.

The built part has these tests:

| Test | Proves |
| --- | --- |
| `internal/runtime/egress/` (unit) | The proxy allows the destinations of `--allow` and refuses the others. |
| `internal/runtime/egress/` and `internal/cmd/` (unit) | The upstream route replaces the client credential with the key, and the key reaches no log, error or response. |
