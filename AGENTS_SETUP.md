# Claude Code subagents

Definitions live in `agents/` (only agent files go there); `.claude/agents` is a symlink to it so Claude Code discovers them.

| Agent | Model | Role |
| --- | --- | --- |
| `orchestrator` | Sonnet 5.5, effort high | Main agent: plans, implements, delegates |
| `reader` | Haiku 4.5 | Maps the repo with /graphify into `.claude/repo-map.md` |
| `code-reviewer` | Haiku 4.5 | Reviews changes since the branch diverged from its base |
| `lint-checker` | Haiku 4.5 | Linters, unused code, style discrepancies (C#, SQL, JavaScript, Bash, plus Go/Python/Rust) |
| `senior-dev` | Opus 5.5, effort high | Advisor for hard problems and design decisions |

`.claude/settings.json` makes `orchestrator` the default main agent for sessions started in this repo
(Sonnet 5.5, high effort). Start it explicitly with `claude --agent orchestrator`.
Subagents cannot spawn other subagents, so the orchestrator must run as the main thread.
