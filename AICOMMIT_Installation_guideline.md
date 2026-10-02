# aicommit – Installation Guideline

`aicommit` generates a commit message for your **staged** changes using any OpenAI-compatible LLM API.
The prompt contains the rules from `.git-commit-rules`, the current branch name and the staged diff.

## Requirements

- Go 1.22+ (standard library only, no external modules)
- git
- An OpenAI-compatible endpoint (OpenAI, Ollama, LM Studio, vLLM, OpenRouter, ...)

## Installation

1. Build the binary and put it on your `PATH`:

   ```bash
   go build -o aicommit .
   mkdir -p ~/.local/bin
   mv aicommit ~/.local/bin/        # or: go install .
   ```

   Make sure `~/.local/bin` is in your `PATH`.

2. Configure the API via environment variables (add to `~/.bashrc` or `~/.zshrc`):

   ```bash
   export AICOMMIT_API_KEY=sk-...                        # optional for local servers
   export AICOMMIT_BASE_URL=https://api.openai.com/v1    # e.g. http://localhost:11434/v1 for Ollama
   export AICOMMIT_MODEL=gpt-4o-mini
   ```

   `OPENAI_API_KEY` and `OPENAI_BASE_URL` are used as fallbacks.

3. Create a `.git-commit-rules` file in the root of each repo you use it in. It is read from
   the repo you are currently in (found with `git rev-parse --show-toplevel`, so it also works
   from subdirectories). Use `-r PATH` to point to a different file. If the file is missing, a default
   Conventional Commits rule is used and a warning is printed. Example:

   ```
   - Use Conventional Commits: type(scope): subject.
   - Subject line max 72 characters, imperative mood, no trailing period.
   - Add a body only for non-trivial changes; explain WHY, not what.
   ```

## Usage

```bash
git add -p            # only staged changes are used
aicommit              # print the generated message
aicommit -c           # generate, open editor to review, then commit
aicommit -H "JIRA-123, fixes login redirect"   # extra context for the model
```

The current branch name (e.g. `feature/JIRA-123-login`) is sent to the model automatically,
so rules such as "prefix the subject with the ticket id from the branch name" work.

## Options

| Option | Description |
| --- | --- |
| `-c` | Commit with the generated message (editor opens to confirm) |
| `-r` | Path to a rules file |
| `-m` | Model name (overrides `AICOMMIT_MODEL`) |
| `--base-url` | API base URL (overrides `AICOMMIT_BASE_URL`) |
| `--api-key` | API key (overrides `AICOMMIT_API_KEY`) |
| `-H` | Extra context for the LLM |

## Notes

- Nothing staged → the tool exits with an error; unstaged changes are never read.
- Diffs larger than 60,000 characters are truncated before being sent.
- The staged diff is sent to the configured API endpoint; use a local model for sensitive code.
