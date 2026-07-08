# How to Check GoKG vs CodeGraph on These Examples

This checklist is for fair, reproducible comparison. Do not claim another tool
cannot do something unless you tested the same fixture, exact tool version, and
specific output surface.

The goal is not to prove "CodeGraph is bad." CodeGraph is a broad polyglot code
intelligence tool. These examples show where GoKG's Go-first graph model exposes
Go-specific relationships directly.

## Fairness Rules

1. Record tool versions.
2. Run tests on a clean copy of the examples.
3. Keep the source code neutral. Do not put expected graph answers in comments.
4. Compare graph evidence, not only AI natural-language answers.
5. Separate "not found in this CLI output" from "impossible for the tool."

```bash
gokg version
CODEGRAPH_BIN="${CODEGRAPH_BIN:-codegraph}"
"$CODEGRAPH_BIN" version
```

When testing a specific local CodeGraph build, set `CODEGRAPH_BIN` to that
binary. Otherwise it defaults to `codegraph` on `PATH`.

Create a temp copy so CodeGraph does not write `.codegraph/` directories into
the repository:

```bash
TMPDIR="$(mktemp -d /tmp/gokg-codegraph-check.XXXXXX)"
cp -R examples/beyond-ast "$TMPDIR/beyond-ast"
```

## Example 1: Implicit Interface

Target relationship:

```text
StripeProcessor -[IMPLEMENTS]-> PaymentProcessor
```

### Check with GoKG

From the GoKG repository root:

```bash
gokg analyze --db /tmp/gokg-beyond-ast-check --rebuild --tests --gc=false

gokg query --db /tmp/gokg-beyond-ast-check \
  'MATCH (s:STRUCT)-[r:IMPLEMENTS]->(i:INTERFACE)
   WHERE s.Name = "StripeProcessor"
   RETURN s.Name, i.Name
   LIMIT 10'
```

Expected:

```json
[
  {
    "i.Name": "PaymentProcessor",
    "s.Name": "StripeProcessor"
  }
]
```

### Check with CodeGraph

```bash
cd "$TMPDIR/beyond-ast/01-implicit-interface"
"$CODEGRAPH_BIN" init .
"$CODEGRAPH_BIN" node PaymentProcessor
"$CODEGRAPH_BIN" query PaymentProcessor
```

What to look for:

- Does `codegraph node PaymentProcessor` list concrete implementers?
- Does it expose an implementation edge equivalent to `IMPLEMENTS`?
- Does it only show the interface members and same-name methods?

Observed with CodeGraph `1.0.1`: `codegraph node PaymentProcessor` shows the
interface and its `Process` member, but does not list `StripeProcessor` as an
implementer in that output.

Observed with CodeGraph `1.3.0`: same result on this fixture.
`codegraph node PaymentProcessor` shows only the interface and its `Process`
member. `codegraph explore "What implements PaymentProcessor?"` returns the
relevant source containing `StripeProcessor`, but does not expose a first-class
`IMPLEMENTS` edge in the CLI output.

Safe claim:

```text
GoKG exposes implicit Go interface implementations as first-class graph edges.
```

Avoid this claim unless retested against the current CodeGraph release:

```text
CodeGraph cannot understand Go interfaces.
```

## Example 2: Goroutines and Channels

Target relationships:

```text
Dispatcher -[SPAWNS]-> goroutine
Dispatcher -[SENDS_TO]-> workQueue
worker     -[RECEIVES_FROM]-> workQueue
```

### Check with GoKG

```bash
gokg query --db /tmp/gokg-beyond-ast-check \
  'MATCH (f:FUNC)-[r:SPAWNS]->(g:GOROUTINE)
   WHERE f.Name = "Dispatcher"
   RETURN f.Name, g.Name
   LIMIT 10'

gokg query --db /tmp/gokg-beyond-ast-check \
  'MATCH (f:FUNC)-[r:SENDS_TO]->(c:CHANNEL)
   WHERE f.Name = "Dispatcher"
   RETURN f.Name, c.Name
   LIMIT 10'

gokg query --db /tmp/gokg-beyond-ast-check \
  'MATCH (f:FUNC)-[r:RECEIVES_FROM]->(c:CHANNEL)
   WHERE f.Name = "worker"
   RETURN f.Name, c.Name
   LIMIT 10'
```

Expected result shapes:

```json
[
  {
    "f.Name": "Dispatcher",
    "g.Name": "goroutine_L15_C3"
  }
]
```

```json
[
  {
    "c.Name": "workQueue (chan string)",
    "f.Name": "Dispatcher"
  }
]
```

### Check with CodeGraph

```bash
cd "$TMPDIR/beyond-ast/02-concurrency"
"$CODEGRAPH_BIN" init .
"$CODEGRAPH_BIN" node Dispatcher
"$CODEGRAPH_BIN" query workQueue
```

What to look for:

- Does CodeGraph expose a goroutine node?
- Does it expose a channel node for `workQueue`?
- Does it expose send/receive edges separately from ordinary function calls?

Observed with CodeGraph `1.0.1`: `codegraph node Dispatcher` shows the source
and a `Calls -> worker` trail, while `codegraph query workQueue` returns no
symbol result.

Observed with CodeGraph `1.3.0`: same result on this fixture.
`codegraph node Dispatcher` shows source plus `Calls -> worker`, while
`codegraph query workQueue` and `codegraph query goroutine` return no symbol
result.

Safe claim:

```text
GoKG exposes goroutines and channel send/receive flows as first-class graph nodes and edges.
```

Avoid this claim unless retested:

```text
CodeGraph cannot reason about concurrency at all.
```

## Example 3: Multi-Repo Workspace

Target question:

```text
Which services call methods owned by example.com/acme/shared-libs?
```

### Check with GoKG

```bash
gokg workspace init go-microservices-demo
gokg workspace add --workspace go-microservices-demo ./examples/beyond-ast/03-multi-repo-workspace/shared-libs
gokg workspace add --workspace go-microservices-demo ./examples/beyond-ast/03-multi-repo-workspace/order-service
gokg workspace add --workspace go-microservices-demo ./examples/beyond-ast/03-multi-repo-workspace/payment-service

gokg analyze --workspace go-microservices-demo --rebuild --tests --gc=false

gokg query --workspace go-microservices-demo \
  'MATCH (caller:FUNC)-[r:CALLS]->(callee:METHOD)
   WHERE callee.RepoID = "example.com/acme/shared-libs"
   RETURN caller.Name, caller.RepoID, callee.Name, callee.PkgPath
   LIMIT 20'
```

Expected: callers from both `order-service` and `payment-service`, with `RepoID`
preserved in the result.

### Check with CodeGraph: Separate Project Indexes

```bash
cd "$TMPDIR/beyond-ast/03-multi-repo-workspace/shared-libs"
"$CODEGRAPH_BIN" init .

cd "$TMPDIR/beyond-ast/03-multi-repo-workspace/order-service"
"$CODEGRAPH_BIN" init .

cd "$TMPDIR/beyond-ast/03-multi-repo-workspace/payment-service"
"$CODEGRAPH_BIN" init .

cd "$TMPDIR/beyond-ast/03-multi-repo-workspace/shared-libs"
"$CODEGRAPH_BIN" callers Validate
```

Observed with CodeGraph `1.0.1`: from the separate `shared-libs` index,
`codegraph callers Validate` does not show callers from `order-service` or
`payment-service`.

Observed with CodeGraph `1.3.0`: same result for separate project indexes.
The parent-folder index below is required for CodeGraph to see all three
folders together in this fixture.

### Check with CodeGraph: Parent-Folder Index

```bash
cd "$TMPDIR/beyond-ast/03-multi-repo-workspace"
"$CODEGRAPH_BIN" init .
"$CODEGRAPH_BIN" callers Validate
"$CODEGRAPH_BIN" impact Validate
```

Observed with CodeGraph `1.0.1`: when indexing the parent folder that contains
all three modules, CodeGraph finds callers such as `ProcessOrder` and
`ConsumeOrderCreated`.

Observed with CodeGraph `1.3.0`: parent-folder indexing finds the callers and
impact chain:

```text
Callers of "Validate" (2):
- ProcessOrder in order-service/handler.go
- ConsumeOrderCreated in payment-service/consumer.go
```

`codegraph impact Validate` reports affected symbols in all three folders:
`shared-libs/event.go`, `order-service/handler.go`, and
`payment-service/consumer.go`.

This means the fair claim is not:

```text
CodeGraph cannot find cross-repo callers in this example.
```

The fair claim is:

```text
GoKG has an explicit multi-repo workspace model with repo IDs, workspace queries,
and workspace-oriented workflows. CodeGraph can index a containing folder and
find callers/impact in this fixture, but the public CLI workflow is project-index
or folder-index oriented rather than Go workspace/repo-ID oriented.
```

This fixture is intentionally stored under one Git root so it can live inside
the GoKG repository. Use it for workspace graph queries. Test
`gokg impact --workspace` with real workspace entries that each have their own
Git root.

## Summary of Safe Claims

Use these in public copy:

- GoKG exposes implicit Go interface implementations as first-class graph edges.
- GoKG exposes goroutines and channel send/receive flows as first-class graph nodes and edges.
- GoKG has explicit multi-repo workspaces with `RepoID`.
- GoKG workspace impact analysis should be validated with real multi-Git-repo workspaces, not this single-Git-root fixture.
- CodeGraph is broader and polyglot; GoKG is narrower and Go-first.

Do not use these without retesting:

- CodeGraph cannot understand Go.
- CodeGraph cannot find cross-repo callers.
- CodeGraph cannot answer architecture questions.
