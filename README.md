# Schema Registry for Low-Code Forms

A centralized, versioned schema registry for low-code form definitions. Form
structures evolve continuously; this registry keeps the definitions in one
place so that frontends, backends and stored data always agree, and rejects
incompatible changes **before** they can break online validation and
rendering.

## Features

1. **Versioned subjects.** Schemas are registered under a *subject* and each
   successful registration produces a new, monotonically increasing,
   **immutable** version. You can read a specific version or the latest;
   historical content is never rewritten. Each stored version also carries a
   SHA-256 content fingerprint and compatibility metadata.
2. **Pre-registration compatibility checking** with four modes
   (`NONE`, `BACKWARD`, `FORWARD`, `FULL`), configurable per subject.
   Incompatible registrations are rejected and no existing version is
   modified.
3. **Safe concurrency.** All mutations are linearized by a single mutex and
   persisted atomically. Concurrent registrations to one subject produce
   dense, non-repeating version numbers with no holes, and a failed
   registration never leaves a half-finished version — the result is
   identical to serial execution.
4. **Full-instance validation** against any specific version: required
   fields, primitive types, value ranges, enums and nested structures
   (objects and arrays). Validation aggregates **all** errors with
   JSON-pointer-like paths (`$.address.zip`, `$.tags[1]`) instead of
   returning on the first failure.
5. **Cross-subject references** with automatic pinning. A reference may name
   a subject only ("latest") or subject + version; at registration time every
   reference is resolved and **pinned** to the concrete version, so stored
   documents never drift. Reference cycles (direct, indirect and
   self-references) are detected at registration and rejected. Deleting a
   version that is still referenced is rejected.
6. **Persistence and restart recovery.** Committed versions and their
   metadata are written to an atomic snapshot (`snapshot.json`, temp file +
   `fsync` + rename); reopening the data directory restores everything. An
   interrupted registration leaves no intermediate state because state is
   only persisted after every check succeeds.
7. **Authentication, authorization and auditing.** Bearer tokens map to
   roles (`ADMIN`, `WRITER`, `READER`); every operation (including denied
   attempts) is appended to an audit log with actor, action, subject,
   version, outcome and UTC timestamp.
8. Automated tests cover compatibility rejection, concurrent registration,
   references/cycles, deletion protection, restart recovery and auth/audit.

## Packages

| Package      | Responsibility                                                        |
|--------------|-----------------------------------------------------------------------|
| `schema`     | Form schema model, definition validation, fingerprints, ref extraction |
| `compat`     | BACKWARD / FORWARD / FULL compatibility diffing                       |
| `validator`  | Instance validation with aggregated, path-localized errors            |
| `auth`       | Token authentication and role-based authorization                     |
| `audit`      | Append-only JSONL file audit log (+ in-memory log for tests)          |
| `registry`   | Registry core: versions, references, persistence, auth & audit hooks  |

## Schema model

A schema has an `id`, `name` and a list of fields. A field is:

```json
{
  "name": "age",
  "type": "integer",
  "required": true,
  "min": 0,
  "max": 150,
  "enum": [18, 30, 60]
}
```

Supported types: `string`, `integer`, `number`, `boolean`, `object`, `array`.

- `object` fields use `properties` (nested field list).
- `array` fields use `items` (a field descriptor).
- `min`/`max` are numeric ranges for `integer`/`number`, **length** bounds
  for `string` and `array`.
- `enum` restricts allowed values; enum elements must match the field type.
- An object field may use `ref` instead of inline properties:

```json
{ "name": "shipTo", "type": "object", "ref": { "subject": "addresses" } }
```

See [`examples/order.schema.json`](examples/order.schema.json).

## Compatibility semantics

Compatibility is checked between the **latest registered version** of a
subject and the registration candidate. The first version of a subject always
passes, and `NONE` disables checking.

### BACKWARD (default)

New data readers using the new schema must be able to read data written under
the old schema.

| Change                        | Allowed? | Reason                                   |
|-------------------------------|----------|------------------------------------------|
| Add optional field            | ✅        | Old data simply lacks it                 |
| Add required field            | ❌        | Old data has no value for it             |
| Remove field                  | ✅        | New readers ignore the leftover value    |
| Make optional → required      | ❌        | Existing rows may miss it                |
| Change type                   | ❌        | Values cannot be interpreted             |
| Widen range (`min`↓ / `max`↑) | ✅        | Every old value still valid              |
| Narrow range                  | ❌        | Some old values become invalid           |
| Remove enum value             | ✅        | Old values might not appear again        |
| Add enum value                | ❌        | Old data may already carry the new value |

### FORWARD

Old readers using the old schema must be able to read data written under the
new schema — roughly the mirror image of BACKWARD.

| Change                              | Allowed? |
|-------------------------------------|----------|
| Add field (required or optional)    | ✅        |
| Remove optional field               | ✅        |
| Remove required field               | ❌        |
| Make optional → required            | ❌        |
| Change type                         | ❌        |
| Narrow range                        | ✅        |
| Widen range                         | ❌        |
| Add enum value                      | ✅        |
| Remove enum value                   | ❌        |

### FULL

Both BACKWARD and FORWARD must hold. Concretely: adding/removing **optional**
fields is allowed in both directions, adding/removing **required** fields is
not, types and constraints must stay compatible in both directions, and enum
sets and ranges must effectively remain unchanged.

Nested objects are compared recursively. **References** are expanded to their
pinned target versions and compared recursively; if a referenced subject
switches to an incompatible referenced version, the registration is rejected.

### Configuration

```go
reg.SetConfig("user-profile",
    schema.SubjectConfig{Compatibility: schema.CompatFull}, adminToken)
```

`Compatibility` is one of `NONE`, `BACKWARD`, `FORWARD`, `FULL`
(`schema.Compat*`). The setting applies to subsequent registrations; already
committed versions record and keep the mode that was active when they were
created. Subjects without explicit configuration use `BACKWARD`.

## References and deletion rules

- References are pinned at registration time; unversioned references resolve
  to the current latest version.
- A registration creating a reference cycle is rejected with
  `ErrReferenceCycle`.
- `DeleteVersion` succeeds only when:
  1. the version is the **latest** (deleting older versions would create
      holes), and
  2. no committed version (tip or historical) references it.

  Delete versions newest-first; when the last version is deleted the subject
  is removed.

## Persistence layout

```
<data-dir>/
  snapshot.json     # all subjects, versions, configs (atomic rewrite)
  audit.jsonl       # append-only audit events, one JSON object per line
```

## Minimal usage

```go
authz := auth.NewAuthorizer()
authz.RegisterToken("writer", "form-service", auth.RoleWriter)

reg, _ := registry.New("./data", authz, auditLog)
defer reg.Close()

v, err := reg.Register("user-profile", mySchema, "writer")
err = reg.Validate("user-profile", v.Version, instanceJSON, "reader")
```

Run the end-to-end demo:

```bash
go run ./cmd/schema-registry            # temp dir
go run ./cmd/schema-registry ./data     # persistent; run twice to see recovery
```

## Tests

```bash
go test ./...
go test -race ./...
```
