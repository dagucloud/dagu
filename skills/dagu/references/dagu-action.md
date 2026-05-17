# Remote Action Packages

Use this reference when creating a reusable package-style action with `dagu-action.yaml`.

Remote actions are different from DAG-local `actions:` templates:

- DAG-local `actions:` are inline wrappers around built-in actions.
- Remote actions are directories or Git repositories that contain a manifest, a DAG entrypoint, and any helper files the action needs.
- Callers use them with `action: owner/repo@version`, `action: name@version`, or `action: source:target@version`.

## Package Layout

```text
dagu-action-notify/
├── dagu-action.yaml
├── workflow.yaml
└── scripts/
    └── notify.sh
```

This reference uses `workflow.yaml` as the recommended entrypoint DAG filename to keep it visually distinct from the `dagu-action.yaml` manifest. The `dag` field can point to any safe relative file path inside the package.

`dagu-action.yaml` supports exactly these fields:

- `apiVersion` - required, currently `v1alpha1`
- `name` - required action name
- `dag` - required relative path to the action DAG file
- `inputs` - optional JSON Schema object for the caller's `with:`
- `outputs` - optional JSON Schema object for the action output object

Unknown manifest keys are rejected. The `dag` path must resolve to a file inside the package.

## Manifest Example

```yaml
apiVersion: v1alpha1
name: notify
dag: workflow.yaml
inputs:
  type: object
  additionalProperties: false
  required: [text]
  properties:
    text:
      type: string
outputs:
  type: object
  additionalProperties: false
  required: [messageId]
  properties:
    messageId:
      type: string
    status:
      type: string
```

`inputs` validates the caller's `with:` object before the action DAG starts. JSON Schema `default` values are validated as schema defaults, but they are not applied to the caller's `with:` object before parameters are passed.

## Action DAG

The action DAG is a normal Dagu workflow. Do not set `working_dir` in the action DAG or local sub-DAGs inside the package; Dagu runs them in the materialized action workspace so relative package files are available.

```yaml
tools:
  - jqlang/jq@jq-1.7.1

params:
  - text
steps:
  - id: send
    run: ./scripts/notify.sh "${text}"
    stdout:
      outputs:
        fields:
          messageId:
            decode: json
            select: .id
          status:
            decode: json
            select: .status
```

Scalar `with:` fields are passed as runtime parameters and can be read as `${text}`. For structured input, pass an explicit JSON string and decode it in the action DAG; do not assume nested YAML/JSON input objects arrive as structured params.

## Tools

If the action DAG invokes portable external CLIs, declare them with top-level `tools` in the action DAG file. Do not put `tools` in `dagu-action.yaml`; unknown manifest keys are rejected.

Caller DAG tools are not inherited by remote actions. The action DAG is a separate DAG run, and the worker running it prepares that DAG's tools in the worker-local tools cache. Built-in-only actions do not need `tools`, but reusable action packages that call binaries such as `jq`, `yq`, or release helpers should pin those dependencies inside the action DAG.

## Returning Outputs

Use `stdout.outputs` when a command emits the action result on stdout:

```yaml
steps:
  - id: classify
    run: ./classify.sh "${text}"
    stdout:
      outputs:
        fields:
          label:
            decode: json
            select: .label
          confidence:
            decode: json
            select: .confidence
```

Use `outputs.write` when the result is assembled from parameters, previous step output, or literals:

```yaml
steps:
  - id: send
    run: ./scripts/notify.sh "${text}"
    output:
      response:
        from: stdout
        decode: json

  - id: publish
    depends: [send]
    action: outputs.write
    with:
      values:
        messageId: ${send.output.response.id}
        status: sent
```

Do not use object-form `output:` to return data to the parent DAG. Object-form `output:` is step-scoped inside the action DAG and is read as `${step_id.output.*}`. To cross the action boundary, republish values with `stdout.outputs` or `outputs.write`.

If the manifest declares `outputs`, Dagu validates the final collected action output object after the action DAG returns a run result. Validation failure fails the parent action step. The action executor also writes compact output JSON to the action step stdout for compatibility, but callers should read structured values through `${step.outputs.<field>}`.

Compatibility note: if an action DAG publishes no typed outputs, legacy string-form run outputs from `output: NAME` can be carried as action outputs. New action packages should prefer `stdout.outputs` or `outputs.write` because those define the action boundary explicitly.

## Caller Example

```yaml
steps:
  - id: notify
    action: acme/dagu-action-notify@v1.2.0
    with:
      text: "Build ${BUILD_ID} finished"

  - id: audit
    depends: [notify]
    run: echo "Message ID: ${notify.outputs.messageId}"
```

## References And Workers

Reference formats:

- `name@version` - official Dagu action, resolved as `dagucloud/action-name`
- `owner/repo@version` - GitHub repository
- `source:target@version` - explicit local path, `file://` path, or Git source

Use immutable tags or commit SHAs for production. Local `source:` paths are useful for development and shared-volume workers only when the worker executing the action can read the same path. For shared-nothing or heterogeneous workers, prefer GitHub or explicit Git `source:` refs. After resolution, Dagu packages the action workspace and can send that bundle to the worker running the child action DAG.
