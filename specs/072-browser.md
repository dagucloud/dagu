# Spec: Browser Actions

## Status

Partially implemented.

Conformance covers extraction into outputs, a multi-operation run with model
and fixed conditions, screenshots, downloads, uploads, the replay cache, a
replay under `ai: never` with no model, the
secret check, allowed domains, client certificates, dialogs, and validation. Waiting for input and resuming the same
browser need the REST API and are covered by integration tests. Model prompts,
page interaction internals, and profile locking belong to executor tests.

## Scope

This spec defines the `browser.extract` and `browser.run` action boundary: the
`with` contract, model configuration, outputs, artifacts, downloads, uploads,
the replay cache, allowed domains, and human input. It does not define how a model chooses
page elements or the browser runtime's prompts.

## Goal

Workflow authors can drive a website that has no API from a step: act on pages
in natural language, upload local files, extract structured data into step
outputs, check page state, and pause for a person when the site asks for
input.

## Behavior

### Actions

`browser.run` takes `with.do`, a nonempty list of operations run in order in one
browser session. `with.url`, when set, is opened before the first operation.

`browser.extract` takes `with.url`, `with.instruction`, and `with.schema`, and
behaves as `browser.run` with one `extract` operation.

Each operation sets exactly one of:

- `goto`: navigate to a URL.
- `act`: perform one action described in natural language, such as a click
  or typing into one field. The value is an instruction string or an object
  with `instruction` and optional `ai` or `cache`.
- `upload`: `{files, into, ai, cache}` gives local files to a file input (see
  Uploads).
- `extract`: `{instruction, schema}`. The schema must be a JSON Schema with
  `type: object`. A string field with `format: uri` or `format: uri-reference`
  gets the address of the element the model picks, such as a link's target;
  any other string field gets what the model writes, which for a link is its
  element id, such as `0-11`.
- `expect`: a condition that must hold; otherwise the step fails with the
  reason.
- `wait`: `{selector}` waits until the element is visible; `{duration}` pauses.
- `screenshot`: a name; the page is saved as a PNG run artifact.
- `ask`: `{prompt, as, timeout}` waits for a person's answer (see Human input).

A `goto`, an `upload`, or an `act` performed or replayed, ends once the page
settles, so the next operation reads what it brought: a page it loaded waits until its network
is idle, and a page it changed in place until its requests end and it stops
changing for half a second. A page that has not settled after five seconds is
read as it is.

Any operation may set `when`, a condition checked once before the operation;
the operation is skipped unless it holds. Any operation may set `timeout`, a
duration such as `30s`; the default is two minutes. A browser that stops
responding fails the step: an operation is abandoned a few seconds after its
timeout, and a screenshot or page check after 30 seconds.

### Uploads

`upload.files` is a string or a nonempty list of strings. Each names files
in one of these forms, after references are filled in:

- a path, or a glob pattern such as `invoices/*.pdf`, whose matches are taken
  in sorted order and of which at least one must match. A relative path is
  under the step's working directory, and a leading `~` is the home
  directory of the user running the step;
- several paths or patterns, one per line;
- JSON as an earlier step publishes it: a list of paths, of objects with a
  `path`, or of `mail.search` emails, whose attachments the files are; or one
  such object. An object without a `path`, such as an attachment that was
  not saved, fails the step, naming `save_attachments: true`.

The files are uploaded in the order named, each once. A file that does not
exist, a directory, or a file over 50 MB fails the operation before the page
is touched.

`upload.into` describes, in plain words, the file input, or the button or
drop area that opens the file picker. The model finds the element once,
asked with the page's elements and the `into` text; the step records it and
later runs replay it as acts are replayed (see Replay cache). The element
gets the files directly when it is a file input or holds exactly one;
otherwise the step clicks it and gives the files to the file picker the
click opens, which the browser intercepts so no window shows; when no picker
opens within five seconds, the one file input nearest the element, the first
of its ancestors holding any, gets them, and an element with no such single
input fails the operation. Without `into`, the page's only file input gets
the files and no model request is made; a page with none, or with several,
fails the operation, naming the count.

A file input or picker that takes one file fails the operation when given
several, before any file is given. File inputs given the files directly get
them as the browser builds them from the files' contents, with the media
type their extension implies; a file picker gets the files from disk. The
model never receives the files, their names, or their paths.

### Conditions

A condition is either:

- a string: a statement about the page, judged by the model; or
- an object with exactly one of `text` (the page's visible text contains it),
  `selector` (a CSS selector matches a visible element), or `url` (the current
  URL contains it). These fixed checks read the page and make no model request.

A fixed check may set `within`, a duration during which it keeps reading the
page until it holds. A fixed `when` reads the page once unless `within` is set;
a fixed `expect` keeps reading until `within`, or the operation timeout when
`within` is unset. A model-judged condition is asked once. A `selector` check
holds when any matching element is visible.

### Model

A browser step uses the DAG-level `llm` block. `with.llm` replaces it entirely,
with the same shape and providers as the DAG-level block. A browser step with no
model configuration fails validation when some operation can call the model
under its choice of how much AI decides: an `act`, or an `upload` with `into`,
whose choice is not `never`, an `extract`, or an `expect` or `when` that is a
statement. The error names the first such operation. A step none of whose
operations can call the model runs without one. When several models are
listed, each model request tries them in order.

Every model request asks for a tool call whose parameters are the response
schema. A reply without a tool call is accepted when its text is valid JSON.
Any other reply gets one correction: the next request to the same model
carries the rejected reply as an assistant message, followed by a user message
stating why it was rejected, and the step log shows the rejected reply. When
the corrected reply is also unusable, that model has failed and the next
listed model is tried. Models that follow tool-call schemas reliably are required for dependable
results.

### Variables and secrets

`with.variables` maps names to values. An `act` instruction references them as
`%name%`, and so does a later act for an `ask.as` name. A reference to any other
name fails validation. An act that references the answer of an `ask` that was
skipped fails the step. The browser receives variable values; requests to the
model carry only the names.

The step fails before starting a browser when an `act`, `upload` `into`,
`extract`, `ask`, or model-judged `expect` or `when` text contains the resolved value of a secret
declared under `secrets:` that has four or more characters. Values that only
come from `env:` are not secrets and are not checked.

Declared secret values and `ask` answers of four or more characters are masked
in text sent to the model, including the page text, in the step log, and in
the timeline. Other variable values are not masked.

A model request carries the instruction, upload `into` text, or judged
statement, the page's elements with their roles and visible text, and the
extract schema or the names of the available variables. It never carries
variable values, text the browser typed into fields, uploaded files, or
screenshots.

### Outputs

The top-level properties each `extract` schema lists become step outputs,
readable as `${steps.<id>.outputs.<name>}` and known when the DAG loads. Two
extract operations in one step that list the same property fail validation.
Only listed properties are published; extracted fields a schema does not list
are dropped. When the step succeeds with outputs, stdout is one JSON object of
those outputs. Operation progress is written to stderr.

### Browser options

`with.browser` accepts `headless` (default `true`), `executable` (otherwise
`CHROME_PATH` or an installed Chrome), `viewport` `{width, height}`, `proxy`
(unauthenticated), `allowed_domains`, `screenshots`, `profile`, and
`client_certificates`.

A browser without a window sends the user agent its executable sends with
one, never `HeadlessChrome`, so a site treats the step as it treats a person.
The user agent is learned from the executable the first time it runs without
a window, by starting it once on a profile of its own, and kept under the
data directory's `browser/user-agents/`. A launch whose browser reports
another version than the one kept learns it again, and starts the browser
again only when the user agent changed with it. When the user agent cannot be
learned or kept, the step runs and warns. It reports a screen of 1920x1080,
or its viewport's size when that is larger, where Chrome would report one
smaller than its window.

The host configuration `browser.sandbox: false`, or the environment variable
`DAGU_BROWSER_SANDBOX=false`, turns off Chromium's sandbox for every browser
the host starts, for hosts where the sandbox cannot start. The sandbox is on
by default, and a DAG cannot change it. With the sandbox on, a step fails
before starting a browser where the browser runtime would turn the sandbox off
anyway: when `CI` is set, or when Dagu runs as root on Linux.

### Client certificates

`client_certificates` lists TLS client certificates and the hosts each is
presented to, so a site that requires one opens without the operating system
asking for the certificate's password. An entry names `hosts`, either
`pfx_file` with `passphrase` or `passphrase_file`, or `cert_file` and an
unencrypted `key_file` in PEM, and optionally `ca_file`, PEM certificates
trusted for those hosts' servers besides the system's. A host is a host name
or IP address, `*.example.com` for the subdomains of `example.com`, or `*` for
every host. The step's relative files resolve against its working directory.

The host configuration `browser.client_certificates` takes the same entries,
with `passphrase_file` for a passphrase, and applies to every browser the host
starts, including `dagu browser session` browsers. A step's entries come
first, and the first entry whose hosts match a request is used. An entry with
no hosts, a malformed host, both or neither of the file forms, or a passphrase
for a PEM key fails validation; host configuration entries fail at startup.

The certificates are read before the browser starts: an unreadable file, a
wrong passphrase, or a key that does not match its certificate fails the step
then, naming the entry without its passphrase. A certificate outside its
validity period is a warning. A passphrase written in a step is masked and
kept from the model like a secret.

Dagu presents the certificates through a proxy process that runs as long as
the browser. The browser sends it the HTTPS requests to the entries' hosts, and
the proxy reaches those hosts the way the device does: through `proxy` when it
is set, otherwise through the operating system's proxy settings (the proxy
auto-config, automatic detection on Windows, or the manual proxies; the proxy
environment variables on Linux). As in Chrome, it moves to the next route of
a PAC result only when a proxy cannot be reached, drops the connection before
answering, or is a SOCKS proxy that fails; an HTTP proxy that answers, denying
the site or asking for credentials, is not bypassed through a later route. The browser sends every other request there
itself. Chrome does not consult a PAC script for `localhost`, `*.localhost`,
the Windows name `loopback`, loopback addresses (`127.0.0.0/8` and `::1`), or
link-local addresses, and `*` leaves them to the browser. When a certificate
or trusted authority names one of those destinations, the browser sends every
request to the certificate proxy, with that implicit bypass removed, so the
destination reaches it. The certificate proxy reaches those destinations directly, never
through `proxy` or the device's proxy settings, and resolves `localhost` and
`*.localhost` to the loopback addresses itself. Every other request, HTTPS or
not, still follows the device's proxy settings, applied by the certificate
proxy, and is not given a configured client certificate or authority. A site
the certificate proxy cannot reach then closes the browser's connection
instead of failing the proxy request. A device proxy that asks for
credentials cannot be handed back to the browser while every request goes to
the certificate proxy. An HTTPS upstream proxy
may listen on a loopback address; its certificate is checked against
authorities that name the proxy's host, separately from the website.
With certificates, `proxy` takes only `[scheme://]host[:port]` with
scheme `http`, `https`, or `socks5`. A proxy that asks for authentication
cannot carry requests to certificate hosts, and the proxy, not the browser,
verifies those hosts' server certificates.

While a browser runs, any process on the host, including another operating
system user's, can use its certificates: through the browser's DevTools
address, which already lets a local process drive the browser, or through the
proxy, which listens on the loopback interface. Present certificates only on
hosts whose users are trusted with them.

While the browser presents certificates, it also guards its other HTTPS
requests, so that the browser never asks the operating system for a
certificate, which would wait on a system prompt nobody answers. The proxy
makes a TLS handshake of its own with each site, once per host and port
while the browser runs, and sends nothing over it. A site that chose TLS 1.3,
asked for no client certificate, and showed a certificate that a root of
Mozilla's program vouches for gets the browser's own TLS: the proxy passes
the browser's hello to it and lets the answer through once that answer
selects TLS 1.3, so the site sees the browser's handshake, and the browser
verifies the site. The proxy carries every other site itself, presenting no
certificate: one that asks for a certificate, uses TLS 1.2 or older, or has
a certificate only the system's or an entry's authorities vouch for, and,
for a minute before it tries again, one whose handshake failed. It takes
over a connection that cannot pass without the browser noticing. Under TLS
1.3 a request for a certificate is encrypted, so a site that asks the
browser but did not ask the proxy reaches the browser's certificate
selection; a site that refuses the proxy's handshake stays unreachable
through the guard. The host configuration
`browser.guard_client_certificates` (or `DAGU_BROWSER_GUARD_CLIENT_CERTIFICATES`)
turns the guard off, or on for every browser even without certificates. When
the device's proxy asks the certificate proxy for credentials it cannot give,
such as a Windows sign-in, the guard stops and the browser reaches those sites
itself; the step's log says so. For the sites it carries, the guard trusts
the system's CA certificates and every entry's `ca_file`.

A page that does not load because of a client certificate fails the
operation that opened it: the error names what the proxy saw, such as the
site rejecting the certificate, or says the site asks for a certificate and
none is configured for it. That includes a site that answers with an error
page, such as nginx's 400 or IIS's 403.7, rather than refusing the
connection. A site that asks for a certificate and opens without one is not a
failure. An operation that times out names the connections the proxy still
waits on and how far each got. The step's log records each host the proxy
presented a certificate to or verified with a configured authority, each
site that asked for one, and each failure. A site the guard carries appears
only when it asks for a certificate or fails; one it passes, whose handshake
only the browser reads, never does.

### Trusted server authorities

The host configuration `browser.trusted_cas` lists entries with `hosts` and
`ca_file`, a PEM file of certificate authorities or self-signed server
certificates, without private keys. Text around the certificates, as openssl
writes it, is ignored; a private key, a damaged certificate, or a certificate
that is neither an authority nor self-signed is an error.
It applies to workflow browsers and browser sessions, without requiring a
client certificate. Host patterns have the same syntax as client certificates.
All authorities matching the destination host add to its existing trust;
they do not affect which client certificate is presented. A named host
without a client certificate keeps what the guard trusts, the system's CA
certificates and every client certificate's `ca_file`, whether or not the
guard is on, so adding an entry never removes trust. Redirects,
subresources, and secure WebSockets use their own destination's trust.

These entries never add trust to unrelated hosts through the guard. A named
host always uses the certificate proxy, including when the guard is disabled;
failure of that route does not fall back to the browser's route. An HTTPS
upstream proxy's certificate uses authorities matching the proxy's hostname.
Server hostname, validity, and certificate-chain verification remain enabled.

Localhost names, loopback addresses, and link-local addresses are routed as
described for client certificates, so an authority can verify a website opened
at one of those destinations. An authority named for a loopback address also
verifies an HTTPS upstream proxy listening there, and does not verify a
website at a different host. Ordinary private-network addresses keep using the
PAC script.

Authority files are read when a browser starts. An existing browser retains
its original trust until it closes; a later browser reads the current files.

### Allowed domains

The browser runtime applies `allowed_domains` to the page's HTTP(S) requests,
including scripts, images, and API calls, so a site's CDN and sign-in hosts
must be listed. WebSocket connections are not covered, and the runtime's check
can be bypassed. `example.com` matches only that host; `*.example.com` matches
its subdomains but not `example.com`. An entry with a scheme, port, or path, a
`*` other than a leading `*.`, or fewer than two labels fails validation.

Dagu itself checks only the page URL: a `goto` or `with.url` outside the list
fails before navigating, and after the start URL and after every operation the
step fails when a redirect or an action left the allowed domains. Pages without a network
host, such as `about:blank`, are not checked.

Requests the runtime blocks are counted per host. When the start URL or an
operation causes blocked requests, the timeline and the step log show a line
after it such as `allowed_domains blocked 14 requests: cdn.example.com (12),
sso.example.com (2)`, naming up to ten hosts, most blocked first. A failed
step's error ends with the same summary of every request blocked during the
attempt, since a page missing a script or a sign-in redirect usually fails a
later operation. A request blocked after its operation returns is counted with
the next one. When counting stops, for example because the browser's DevTools
connection closes, the step log warns once.

### Refused pages

A page counts as refused when its site answered with a bot check in place of
it, which for Cloudflare is any answer with the header `cf-mitigated:
challenge`, or answered `403` or `429`, with a body or without one, which
Chrome shows as its own error page. A page a client certificate kept from
opening is not counted; Client certificates explains it.

After the start URL and after every operation, a refused page adds a line to
the timeline and the step log, such as `page refused shop.example.com
answered with a Cloudflare bot check (HTTP 403) in place of the page` or
`page refused portal.example.com refused the page (HTTP 403 Forbidden)`, once
while the browser keeps showing it. A step that fails while the browser
shows a refused page ends its error with the same sentence, since a refusal
usually fails a later operation, and keeps the recordings it replayed, since
the refusal says nothing about them.

An action that leaves the browser on a bot check, such as `goto` or an `act`
that loads a page, waits up to 10 seconds for the check to pass, as a check
that needs no person does by loading the page it stood in for, and then
lets the page settle as any new page does. A check still shown after the
wait is left to the operations that follow and reported as above.

### Artifacts

A DAG with a browser action enables artifact storage unless it sets
`artifacts.enabled: false`. Files are written under `browser/<step id>/` in the
run's artifacts directory and are not masked.

| `screenshots` | Automatic screenshots |
| --- | --- |
| `on_failure` (default) | When the step fails. |
| `final` | When the step fails, and at the end of a successful step. |
| `each` | After every operation, plus the `final` ones. |
| `never` | None. |

With artifacts disabled, no automatic screenshots are saved, a `screenshot`
operation fails, and the browser refuses downloads.

### Downloads

Files the page downloads are saved under `browser/<step id>/downloads/` with
the name the site suggests, made unique within the directory. Only `act` and
`goto` operations start downloads. Once one has run, the step waits for running
downloads after every operation, and before it ends or pauses for an `ask` it
also waits a few seconds for a download to begin. A download may run for the
longest timeout of the acts and gotos run so far. A canceled download, or one
still running at that timeout, fails the step.

### Dialogs

The step accepts every JavaScript dialog a page opens, so a dialog never
blocks the page: `alert`, `confirm`, and `beforeunload` are accepted, and a
`prompt` is answered with its default text. Each accepted dialog appears in
the timeline and the step log after the operation that opened it.

### How much AI decides

`with.ai` chooses how much the model decides for the step's `act` operations
and `upload` operations with `into`, and `ai` on an `act` or an `upload`
replaces it for that operation:

- `every_run`: the model decides every run. A successful `act` or `upload`
  still records what it did.
- `on_miss` (the default): a recorded `act` or `upload` replays without a
  model request while the page still shows its elements, and the model acts
  when a replay fails and repairs the recording.
- `never`: a recorded `act` or `upload` replays, and a miss fails the step.
  No model is called. The failure names the reason: no recording of the
  operation on this host, the recorded action that did not replay, or the
  recorded element of an upload that is gone. The actions replayed before the
  miss have already run on the page, which is left as it is. The recording is
  kept, so a later run under `on_miss` repairs it.

`with.cache: false`, `act.cache: false`, and `upload.cache: false`, from before
the choice existed, mean `every_run`. Setting both `ai` and `cache` on the
step, an act, or an upload fails validation. A statement is judged by the
model, and an `extract` reads the page through the model, so a step whose `ai`
is `never` fails validation with an `extract`, or with an `expect` or `when`
that is a statement. A fixed check is allowed, and so is an `upload` without
`into`, which makes no model request.

Each operation's timeline event records `via`, how it ran: `element` for a
replay of recorded elements, `model` for a model request, `exact` for a fixed
check, and nothing for an operation that decides nothing, such as `goto`.

### Replay cache

A successful `act` records the actions it performed. A later run of the same
step on the same host replays them without a model request when the operation
position, instruction, and page URL without query or fragment match, unless
the act's choice is `every_run`. When a replay fails, the step asks the model
again, or fails under `never` (see How much AI decides).
If the step succeeds, the model's new actions replace the recording; an act
that records no actions, such as one whose click loaded a new document before
it reported back, removes the recording instead, unless another run of the step
replaced it first. A replay fails when a recorded element is gone, is on the
page but not visible, such as a field in a closed dialog, or is no longer the
element the act recorded: the element at the recorded location has another
accessibility role or name, or a select there does not offer the recorded
option. When other elements on the page shared the recorded element's role and
name, such as a Delete button in each row of a list, the text of its row must
also match. Without such a row, the replay fails when the page has more
elements of that role and name than when the act recorded it. An element
without a name is told apart by its role only.

A successful `upload` with `into` records the element the model found, keyed
like an act by operation position, `into` text, and page URL, and kept apart
from an act's recording with the same words. A replay uploads into the
recorded element without a model request when the page still shows it, told
apart as a replayed act's element is; otherwise, under `on_miss`, the model is
asked again and its element replaces the recording.

Runs of a step share its recordings, and each `act` reads them when it runs. A
recording that another run of the step replaced or removed meanwhile, such as
an earlier `foreach` item waiting for the same profile, is not replayed.

The cache covers `act` and `upload`. `extract` and model-judged conditions
make model requests on every run.

`dagu browser cache clear <dag>` removes the recorded acts and uploads of
every step of the DAG, or of one step with `--step <id>`, and reports the
steps it removed.
`DELETE /dags/{fileName}/browser-cache[?step=<id>]` does the same and returns
the removed steps. Removing all of a DAG's history with `dagu rm --history`
also clears its cache; removing only older runs keeps it. Each clears the cache
on its own host only. After a clear, the next run of the step makes a model
request and records again.

`dagu browser session export` writes the recordings of the acts and uploads a
browser session ran for a step that has not run yet (Spec 079).

### Profiles

`browser.profile` names a persistent browser profile kept on the executing
host. Cookies and storage survive across runs on that host. Runs that use the
same profile run one at a time; a run fails immediately when another run
waiting for input, or a browser session (Spec 079), holds the profile.

### Human input

An `ask` operation puts the step in `Waiting` with a pending question, keeps the
browser open, and ends the step execution. Answering the question from the Web
UI or REST API resumes the step in the same browser, on the host that holds it,
at the operation after the `ask`. The answer is available to later `act`
instructions as `%<as>%`. Rejecting the question fails the step. The browser
stays open for `ask.timeout` (default one hour); an answer after that fails the
step. The pending question carries that deadline as `expiresAt`.

Answers are stored in the run's history like other human input.

On Windows, a step with an `ask` operation fails before starting a browser,
because the browser cannot outlive the step process there.

## Errors

A missing `with.do`, `with.url` for `browser.extract`, `with.instruction`, or
`with.schema` fails validation with a diagnostic naming the field. An operation
or condition that sets zero or several keys fails validation. A step fails when
an `act` does not complete, an `expect` does not hold, a selector does not
appear before the timeout, a download does not finish, the page leaves the
allowed domains, the browser stops responding, or the browser cannot be
started. An `act` or an `upload` under `never` fails when it has no recording
or its replay misses, as described under How much AI decides. When an `act`
fails because its model chose no element, the error names the model, since
some models give that answer for every request. An
`act` whose model picks an option that the select does not offer does not
complete. Its error names the option compared, filled in with the values of
the variables it references, and up to 15 options the select offers. A
variable whose value holds a declared secret or an `ask` answer, of any
length, is named without its value.
An `upload` fails when a file cannot be uploaded, the model finds no
element for `into`, or the element leads to no single file input, as
described under Uploads. A page that does not load because of a client certificate fails the
operation that opened it, as described under Client certificates. A failure
on a page the site refused names the refusal, as described under Refused
pages.

Loading a DAG warns, without failing, about each `extract` string field whose
name ends in `url`, `uri`, `link`, or `href`, singular or plural, and that has
neither address format, naming the operation and the field's path, such as
`do[0]: extract field "listings[].link"`.

The browser can lose its connection to the page before an `act` or a replay
reports back, as when a click or a chosen option loads a new page. The step
then checks the page instead of acting blindly, which could submit a form
twice: a new document means the action took effect, and the step goes on; the
same document means it did not, and the act runs once more. Losing the page
again, or a page that cannot be read, fails the step. So does losing the page
to a frame that loads a new document while the page keeps its own: the action
may have caused that load, so acting again could repeat it.

## Examples

```yaml
secrets:
  - name: SHOP_COUPON
    provider: env
    key: SHOP_COUPON

llm:
  provider: anthropic
  model: claude-sonnet-5

steps:
  - id: checkout
    action: browser.run
    with:
      url: https://shop.example.com/cart
      browser:
        # Every host the site loads from, including CDNs.
        allowed_domains: ["*.example.com"]
      variables:
        coupon: ${SHOP_COUPON}
      do:
        - act: Type %coupon% into the coupon field
        - act: Click the Apply button
        - act: Click the Place order button
        - expect: {text: Order confirmed}
        - extract:
            instruction: The order number
            schema:
              type: object
              properties:
                order_number: { type: string }

  - id: record
    depends: checkout
    run: echo "${steps.checkout.outputs.order_number}"
```

Uploading the invoices an email brought to a site's file box:

```yaml
llm:
  provider: anthropic
  model: claude-sonnet-5

steps:
  - id: invoices
    action: mail.search
    with:
      mailbox: billing@example.com
      subject: Invoice
      has_attachments: true
      save_attachments: true

  - id: file_box
    depends: invoices
    action: browser.run
    with:
      url: https://books.example.com/receipts
      browser:
        profile: books
      do:
        - upload:
            files: ${steps.invoices.outputs.messages}
            into: The receipt drop area
        - expect: {text: "Uploaded", within: 1m}
```
