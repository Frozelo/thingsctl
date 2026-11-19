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
