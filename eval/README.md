# Tool-surface eval

This eval compares two MCP tool surfaces built from the same command model, using a real agent on a seeded database:

- **tools** (default): `read`, `add`, `update`, `trash`, `batch`, `show`. Every field is described with JSON Schema.
- **ops** (`--surface ops`): `read`, `write(ops[])`, `trash`, `show`, `help`. The fields of `write` are listed as compact text, and full descriptions come from `help`.

Nothing reaches Things. The server runs with `--dry-run`, so every write is validated by the real write code. The simulator (`sim.go`) then applies the validated write to a per-run copy of the seed database, so follow-up reads show the change, and prints a receipt in the real format. The grader turns successful writes into normalized effects and requires exactly the expected set: missing or extra adds, updates or trashes fail the task. Read tasks are graded on the final answer.

The agent is the Codex CLI, isolated from the user's setup with `--ignore-user-config --ephemeral` and with the shell tools disabled, so it can only use the MCP tools.

```sh
EVAL_OUT=/tmp/eval EVAL_REPS=2 EVAL_PAR=6 go test -tags eval ./eval -run TestEval -v -timeout 90m
go test -tags eval ./eval -run 'TestGrader|TestSim'   # offline checks, no agent
```

Settings: `EVAL_SURFACES` (`tools,ops`), `EVAL_TASKS` (a regexp over task names), `EVAL_REPS`, `EVAL_PAR`, `EVAL_MODEL`, `EVAL_EFFORT` (`medium`).

## Results, 2026-11-19

The run used Codex CLI 0.169.0 with its default model at medium reasoning effort: 24 tasks × 2 surfaces × 2 repetitions. There were 6 read tasks, 14 single writes and 4 multi-item tasks.

| surface | schema, o200k tokens | success | mean input tokens | mean uncached | mean tool calls |
|---|---|---|---|---|---|
| tools | 765 | 46/48 | 104,650 | 16,338 | 2.5 |
| ops | 507 | 46/48 | 104,906 | 16,647 | 2.9 |

- **Both surfaces failed only the trash task, and the agent was not at fault.** In non-interactive mode, Codex refuses to call a tool annotated `destructiveHint: true`. Excluding that task, both surfaces scored 46/46.
- **ops saves 258 schema tokens but costs a round trip.** The agent called `help` in 22 of the 36 write runs. Each extra model request in Codex costs about 25k input tokens, mostly cached, so total tokens came out the same on both surfaces.
- **Round trips dominate cost, not schema size.** A one-call read task already uses about 73k input tokens. The schema is about 1% of one request.
- **The agent often re-reads after a write.** On each surface, 24 of the 36 write runs read the item again after the write, even though the receipt already shows the new state.
- **The eval found a real bug.** `add` with `in: "Inbox"` was rejected. It is fixed.

**Decision:** keep the six-tool surface as the default. The ops surface stays behind `--surface ops` for further experiments.

## Round trips, 2026-11-19

The first comparison showed that cost follows the number of model requests. The agent made extra requests in two places:

- **A lookup read before the write**, only to turn a title into an id. This happened in 34 of 34 write runs.
- **A verification read after the write**, in 24 of 36 runs. The receipt usually could not show the change, for example a move to Someday, a new project or appended notes.

Three changes went in as separate commits and were measured cumulatively with `EVAL_VARIANTS`. The run used the same agent setup, all 24 tasks × 2 repetitions, and the tools surface:

- **base** (`ad21d15`): before the changes.
- **receipts** (`737cc9f`): a write receipt says where the item lives (`inbox`, `anytime`, `someday`, `@today`) and its parent (`in:"Kitchen renovation"`). It marks `no-deadline` and repeats changed notes and checklists.
- **trust** (`0598472`): the tool descriptions and server instructions say that the reply is the state read back from Things.
- **titles** (`8798df5`): `update`, `trash`, `show` and `read id` accept an exact title. Matching is case-insensitive and Unicode-aware, open items win over closed ones, and an ambiguous title is an error that lists the candidate ids.

Write tasks, excluding trash (34 runs per variant):

| variant | success | mean input tokens | effective tokens¹ | tool calls | runs that read first | mean seconds |
|---|---|---|---|---|---|---|
| base | 34/34 | 114.4k | 23.8k | 3.09 | 34 | 17.7 |
| receipts | 34/34 | 106.8k | 21.7k | 2.62 | 34 | 18.7 |
| trust | 34/34 | 99.9k | 24.0k | 2.26 | 33 | 17.2 |
| titles | 34/34 | **81.4k** | **20.0k** | **1.47** | 4 | **15.7** |

¹ Effective tokens = uncached input + 10% of cached input + 4 × output, which approximates the price with prompt caching.

- **Single-item writes now take one call.** That is two model requests, about 72–77k input tokens, which is the floor for this client. Before the changes they took 113k.
- **Each change helped.** Receipts and trust removed most verification reads, and titles removed the lookup read.
- **Read tasks and success rates did not change.** Every variant scored 46/48; trash is still blocked by Codex itself.
- **The money saving is smaller than the token count suggests.** Total input fell by 29%, but most of the saved tokens were cached, so the effective cost fell by about 16% and the time by 11%. Uncached input is dominated by Codex's own first request and is noisy from run to run.
- **Multi-item tasks still need a read.** "Mark everything tagged errand as done" must list the items first, and the agent sometimes reads the list again at the end.

## Bulk updates with `where`, 2026-11-19 (preliminary)

After the round-trip fixes, single-item writes were already down to one call. Multi-item tasks still were not, because the agent read the list and then sent one `update` per item. The change in `c7e599f` lets `update --where` replace the id with a selector:

- a list: `inbox`, `today`, `upcoming`, `anytime`, `someday`, `deadlines` or `logbook`;
- `tag:X`, `project:X`, `area:X` or `q:text`.

One call then changes every matching open to-do: never projects, headings or repeating templates, and at most 50 items. The reply ends with how many still match. The eval gained four multi-item tasks: `project_done`, `project_tag`, `area_someday` and `today_done`.

The run compared `8798df5` (before) with `c7e599f` (where) on 28 tasks × 2 repetitions. It stopped early when the Codex account hit its usage limit, so only 65 of 112 runs finished. The numbers below come from the 31 tasks that finished on both variants. The multi-item group has one run per task, so treat it as an early signal rather than a measurement.

| group | variant | success | mean input | effective tokens | tool calls | seconds |
|---|---|---|---|---|---|---|
| multi-item (5 tasks) | before | 5/5 | 104.7k | 18.6k | 4.0 | 25.3 |
| multi-item (5 tasks) | where | 5/5 | **79.8k** | **16.6k** | **1.2** | **16.6** |
| other tasks (26 runs) | before | 26/26 | 78.2k | 19.1k | 1.0 | 14.6 |
| other tasks (26 runs) | where | 26/26 | 75.3k | 17.4k | 1.0 | 14.0 |

- **Four of the five multi-item tasks took one call**, for example `update {where: "today", status: "done"}`. The agent used `where` without being told.
- **The one exception was `project_tag`, and it shows a gap in the receipt.** Adding a tag does not change which items match, so the closing line "now matches 3 to-dos" does not prove that anything happened, and the agent read the project again. A closing line such as "updated 3 of 3" would cover that case.
- **The other tasks did not regress.**
- **Still to do:** a full rerun once the limit resets. `multi_add` and `tag_done` have no data yet.
