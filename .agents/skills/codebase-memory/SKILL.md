---
name: codebase-memory
description: 'Query the codebase-memory knowledge graph via MCP instead of grep/glob when the question is structural rather than textual — "who calls X", "what breaks if I change X", "how does data flow from A to B", "which service calls this endpoint", "what is the architecture here", "where are the performance hot spots". Also use when first exploring an unfamiliar repo, doing impact analysis before a refactor, or when a repo needs indexing before any of the above. Triggers on callers, callees, dependencies, impact analysis, blast radius, data flow, cross-service tracing, architecture overview, dead code, complexity, hot paths, and indexing a repo.'
---

# Codebase Memory

A knowledge-graph index of a repository, served over MCP as `mcp__codebase-memory-mcp__*`.
It parses code into nodes (Function, Method, Class, Route, Variable, File) and edges
(CALLS, IMPORTS, DATA_FLOWS, HTTP_CALLS, ASYNC_CALLS, CROSS_*), so structural questions
get answered by traversal rather than by guessing at grep patterns.

## Use this instead of grep when

| Question | Tool |
|---|---|
| "Where is X defined / what handles Y?" | `search_graph` |
| "Who calls X? What does X call?" | `trace_path` (mode `calls`) |
| "What breaks if I change X?" | `trace_path` inbound + `detect_changes` |
| "How does this value reach there?" | `trace_path` (mode `data_flow`) |
| "Which service calls this endpoint?" | `trace_path` (mode `cross_service`) |
| "What does this repo look like?" | `get_architecture` |
| "Show me the source of X" | `get_code_snippet` |
| "Find this literal string / TODO / config key" | `search_code` (grep, graph-ranked) |
| Anything multi-hop or aggregate | `query_graph` (Cypher) |

Plain `Grep`/`Glob` are still correct for literal text hunting in files the index does not
cover (configs, docs, lockfiles, generated output) and for repos that are not indexed.

## Always check the index first

Every tool except `list_projects` and `index_repository` needs a `project` name.

1. `list_projects` — see what is indexed and get exact project names.
2. If the target repo is absent, `index_repository(repo_path=..., mode=...)`.
3. `index_status(project=...)` to confirm a build finished before querying.

Indexing is a heavyweight operation. Never index without saying what you are about to do,
and do not index a repo the user did not ask about.

### Picking an index mode

- `fast` — filtered files, no similarity/semantic edges. Quickest; `semantic_query` will not work.
- `moderate` — filtered files **plus** similarity/semantic edges. Good default.
- `full` — all files plus similarity/semantic. Largest and slowest.
- `cross-repo-intelligence` — no extraction; only matches Routes/Channels across already-indexed
  projects to create `CROSS_HTTP_CALLS` / `CROSS_ASYNC_CALLS` / `CROSS_CHANNEL` edges.
  Requires `target_projects` (`["*"]` for all). **Index each service first, then run this pass.**

`semantic_query` in `search_graph` requires a `moderate` or `full` index. On a `fast` index it
silently returns nothing useful — use `query` (BM25) instead.

Pass `persistence: true` to write `.codebase-memory/graph.db.zst` so teammates can bootstrap
from the artifact instead of re-indexing.

## Search modes in `search_graph`

Three independent modes, combinable in one call:

- `query="update settings"` — BM25 full-text, splits camelCase (`updateCloudClient` → update,
  cloud, client), boosts Functions/Methods +10, Routes +8, Classes +5. **Best default for
  natural-language discovery.** When set, `name_pattern` is ignored.
- `name_pattern=".*regex.*"` — exact pattern matching.
- `semantic_query=["send","publish","pubsub"]` — **must be an array**, not a string. Each keyword
  scored independently; bridges vocabulary gaps. Results land in `semantic_results`, separate
  from `results`.

Narrow with `label`, `file_pattern`, `min_degree` *before* paginating a large result set.

## Truncation is silent — always check

These tools cap results and will happily hand back a partial answer that reads like a complete one.

- **`search_graph`** — `limit` default 200. Response carries `total` and `has_more`.
  Page with `offset += limit` while `has_more` is true.
- **`search_code`** — `limit` default **10**, and there is **no `offset`**. Compare
  `total_grep_matches` / `total_results` against your limit; to see more, raise `limit` or narrow
  with `file_pattern` / `path_filter`.
- **`query_graph`** — hard 100k row ceiling, no offset. Put `LIMIT` in the Cypher itself, or
  use `search_graph` pagination for browsing.

Before concluding "there are only N of these", verify N is not just the limit.

## Reading source

`get_code_snippet` is a **read** tool, not a search tool. Call `search_graph` first to get the exact
`qualified_name`, then pass it. A short name works but returns suggestions when ambiguous.
`include_neighbors: true` pulls in directly connected nodes.

## Complexity and hot paths

Every `Function` and `Method` node carries queryable performance properties — query them with
`query_graph` rather than eyeballing code:

- `complexity` (cyclomatic), `cognitive`, `loop_count`, `loop_depth`
- `transitive_loop_depth` — worst-case nested-loop degree propagated along CALLS edges
- `linear_scan_in_loop` — find/contains/indexOf inside a loop; **the hidden O(n²) that
  `loop_depth` alone misses**
- `alloc_in_loop`, `recursion_in_loop`, `unguarded_recursion`, `recursive`
- `param_count`, `max_access_depth` — structure smells

```cypher
MATCH (f:Function)
WHERE f.transitive_loop_depth >= 3 OR f.linear_scan_in_loop >= 1
RETURN f.qualified_name, f.transitive_loop_depth, f.linear_scan_in_loop
ORDER BY f.transitive_loop_depth DESC
LIMIT 50
```

## Architecture overview

`get_architecture` accepts `aspects` — use `["overview"]` for a compact summary, omit or `["all"]`
for everything, or pick specific ones (`routes`, `dependencies`, `entry_points`, `hotspots`,
`boundaries`, `layers`, `clusters`, `file_tree`).

`clusters` runs Leiden community detection over the call/import graph and surfaces the *de-facto*
modules with cohesion scores — these routinely cut across the folder layout and are the fastest way
to find the real architectural seams. Scope with `path` to a directory prefix.

## Working practice

- Run `get_graph_schema` once per unfamiliar project before writing Cypher; labels and edge types
  vary by language and index mode.
- Prefer `compact` mode in `search_code` (signatures only) — `full` pulls source and burns tokens.
- `trace_path` filters test files by default; pass `include_tests: true` when tests matter.
- `detect_changes` compares against `base_branch` (default `main`) or a `since` ref, and reports
  impact — run it before a refactor to size the blast radius.
- The graph reflects the last index. After significant edits, re-index or note the staleness rather
  than reporting stale results as current.
- `manage_adr` stores Architecture Decision Records per project (`get` / `update` / `sections`).

## This machine

The server is registered globally in `~/.claude.json` as `codebase-memory-mcp`, running the static
binary at `~/.local/bin/codebase-memory-mcp`. Cache lives in `~/.cache/codebase-memory-mcp`.

`~/Documents/Mediasoft` is a multi-service workspace (CMS-Backend, CMS-Admin, CMS-Public-API,
Media-API, Job-Handler, Streaming, Ingest-App, and others) — each is its own repo and must be
indexed as its own project. Once several are indexed, a `cross-repo-intelligence` pass over
`["*"]` is what makes service-to-service call tracing work across them.
