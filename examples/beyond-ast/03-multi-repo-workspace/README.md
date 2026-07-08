# Multi-Repo Workspace: Go Microservices Example

This example shows GoKG's explicit multi-repo workspace model for Go microservices.

The mock platform has three separate Go modules:

```text
shared-libs/      example.com/acme/shared-libs
order-service/   example.com/acme/order-service
payment-service/ example.com/acme/payment-service
```

`order-service` publishes an `OrderCreated` event from `shared-libs`. `payment-service` consumes the same shared event contract. In a real microservice system, these would often live in separate repositories.

## What Workspace Adds

General-purpose code indexes can inspect a single project or a containing folder. GoKG's workspace mode adds a Go-specific layer on top: every graph node and edge keeps a `RepoID`, so queries can preserve service ownership while traversing Go calls, routes, packages, and methods.

In this fixture:

- `order-service` calls `OrderCreated.Validate`, `Topic`, and `Marshal`.
- `payment-service` calls `OrderCreated.Validate` and `Topic`.
- `shared-libs` owns the event contract and methods.
- Route `/orders` reaches `CreateOrderHandler`, which reaches `ProcessOrder`, which reaches the shared event contract.

For a Go-first backend team, the useful question is not just "what is in this folder?" It is:

> If I change the shared Go event contract, which services and entry points are affected?

GoKG models that as one workspace graph while keeping repository ownership visible in the query results.

## Build the Workspace

From the GoKG repository root:

```bash
gokg workspace init go-microservices-demo

gokg workspace add --workspace go-microservices-demo ./examples/beyond-ast/03-multi-repo-workspace/shared-libs
gokg workspace add --workspace go-microservices-demo ./examples/beyond-ast/03-multi-repo-workspace/order-service
gokg workspace add --workspace go-microservices-demo ./examples/beyond-ast/03-multi-repo-workspace/payment-service

gokg analyze --workspace go-microservices-demo --rebuild --tests
```

`workspace add` uses each module path as the repo ID:

```text
example.com/acme/shared-libs
example.com/acme/order-service
example.com/acme/payment-service
```

## Query Cross-Repo Calls

Find services that call methods owned by `shared-libs`:

```bash
gokg query --workspace go-microservices-demo 'MATCH (caller:FUNC)-[r:CALLS]->(callee:METHOD) WHERE callee.RepoID = "example.com/acme/shared-libs" RETURN caller.Name, caller.RepoID, callee.Name, callee.PkgPath LIMIT 20'
```

The result includes rows like these (order may vary):

```json
[
  {
    "callee.Name": "Topic",
    "callee.PkgPath": "example.com/acme/shared-libs",
    "caller.Name": "ConsumeOrderCreated",
    "caller.RepoID": "example.com/acme/payment-service"
  },
  {
    "callee.Name": "Validate",
    "callee.PkgPath": "example.com/acme/shared-libs",
    "caller.Name": "ConsumeOrderCreated",
    "caller.RepoID": "example.com/acme/payment-service"
  },
  {
    "callee.Name": "Topic",
    "callee.PkgPath": "example.com/acme/shared-libs",
    "caller.Name": "ProcessOrder",
    "caller.RepoID": "example.com/acme/order-service"
  },
  {
    "callee.Name": "Marshal",
    "callee.PkgPath": "example.com/acme/shared-libs",
    "caller.Name": "ProcessOrder",
    "caller.RepoID": "example.com/acme/order-service"
  },
  {
    "callee.Name": "Validate",
    "callee.PkgPath": "example.com/acme/shared-libs",
    "caller.Name": "ProcessOrder",
    "caller.RepoID": "example.com/acme/order-service"
  }
]
```

## Query Route-to-Shared-Contract Flow

Find the HTTP route in `order-service`:

```bash
gokg query --workspace go-microservices-demo 'MATCH (r:ROUTE)-[e:REFERENCES]->(h:FUNC) WHERE h.Name = "CreateOrderHandler" RETURN r.Name, h.Name, h.RepoID LIMIT 10'
```

Then inspect what the handler calls:

```bash
gokg query --workspace go-microservices-demo 'MATCH (handler:FUNC)-[r:CALLS]->(next:FUNC) WHERE handler.Name = "CreateOrderHandler" RETURN handler.Name, next.Name, next.RepoID LIMIT 10'
```

And inspect the shared contract calls:

```bash
gokg query --workspace go-microservices-demo 'MATCH (caller:FUNC)-[r:CALLS]->(shared:METHOD) WHERE caller.Name = "ProcessOrder" AND shared.RepoID = "example.com/acme/shared-libs" RETURN caller.Name, shared.Name, shared.RepoID LIMIT 10'
```

## About Impact Analysis

This fixture keeps three Go modules under one Git root so it is easy to commit as a repository example. Use it to demonstrate workspace graph queries, not Git impact analysis.

For `gokg impact --workspace`, use real repositories where each workspace entry has its own Git root. In that setup, GoKG can map Git diffs to changed graph nodes and report impacted dependents by repository.

## Positioning vs CodeGraph

CodeGraph is a strong general-purpose tool when the goal is broad polyglot code intelligence across many languages and agents. In this fixture, CodeGraph 1.3.0 can find callers and impact when indexing the parent folder that contains all three modules.

This example highlights GoKG's narrower Go-first positioning:

| Question | Generic project index | GoKG workspace |
|---|---|---|
| What is inside one repo? | Works well | Works well |
| Which Go services call this shared Go method? | Can work when indexing a containing folder | Query by `RepoID` in one workspace graph |
| Which route reaches this Go shared contract? | Can surface route/call context | Query route, handler, call, and repo edges together |
| Which repos are affected by a shared Go change? | Symbol impact can work in a containing-folder index | Workspace impact is intended for real multi-Git-repo workspaces |
| What is the center of the model? | Polyglot symbols | Go packages, types, methods, routes, and repos |

The message is not "GoKG replaces CodeGraph." It is:

Use CodeGraph when the primary need is broad polyglot intelligence.

Use GoKG when the primary need is understanding Go architecture across multiple services.
