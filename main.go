// aicommit generates a git commit message for staged changes with an
// OpenAI-compatible LLM, following the .git-commit-rules file of the current repo.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	defaultRules        = "Use Conventional Commits. Subject line <= 72 chars, imperative mood. Optional body explaining why."
	defaultMaxDiffChars = 60000
	systemPrompt        = `You write git commit messages.
Follow these project rules strictly:

<rules>
%s
</rules>

Output ONLY the commit message text. No code fences, no preamble, no explanation.`
)

// Progress and errors go to stderr so stdout only ever carries the message.
func info(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "› "+format+"\n", a...)
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "✗ Error: "+format+"\n", a...)
	os.Exit(1)
}

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// spinner shows an in-progress indicator on stderr until the returned func is called.
func spinner(msg string) (stop func()) {
	if !isTerminal(os.Stderr) {
		info("%s", msg)
		return func() {}
	}
	done, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for i := 0; ; i++ {
			select {
			case <-done:
				fmt.Fprint(os.Stderr, "\r\033[K")
				return
			case <-t.C:
				fmt.Fprintf(os.Stderr, "\r%s %s", frames[i%len(frames)], msg)
			}
		}
	}()
	return func() { close(done); <-finished }
}

// apiHint turns an HTTP status into an actionable suggestion.
func apiHint(status int) string {
	switch status {
	case 401, 403:
		return "check AICOMMIT_API_KEY (or --api-key)"
	case 404:
		return "check AICOMMIT_BASE_URL and AICOMMIT_MODEL (model may not exist at this endpoint)"
	case 429:
		return "rate limit or quota exceeded, try again later"
	}
	if status >= 500 {
		return "the API server had a problem, try again later"
	}
	return ""
}

func git(args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("git", args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func mustGit(args ...string) string {
	out, err := git(args...)
	if err != nil {
		fatal("%v", err)
	}
	return out
}

func env(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// resolveMaxDiff picks the diff size limit: flag, then env var, then default.
func resolveMaxDiff(flagVal int) int {
	if flagVal > 0 {
		return flagVal
	}
	if v := env("AICOMMIT_MAX_DIFF_CHARS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			fatal("AICOMMIT_MAX_DIFF_CHARS must be a positive integer, got %q", v)
		}
		return n
	}
	return defaultMaxDiffChars
}

func loadRules(path, repoRoot string) string {
	explicit := path != ""
	if !explicit {
		path = repoRoot + "/.git-commit-rules"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if explicit {
			fatal("Cannot read rules file: %v", err)
		}
		fmt.Fprintf(os.Stderr, "! %s not found, using default rules\n", path)
		return defaultRules
	}
	info("Loaded rules from %s", path)
	return strings.TrimSpace(string(data))
}

func chat(baseURL, apiKey, model, system, user string) string {
	stop := spinner(fmt.Sprintf("Generating commit message with %s...", model))
	body, _ := json.Marshal(map[string]any{
		"model":       model,
		"temperature": 0.2,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	})
	req, _ := http.NewRequest("POST", strings.TrimRight(baseURL, "/")+"/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := (&http.Client{Timeout: 120 * time.Second}).Do(req)
	if err != nil {
		stop()
		fatal("Cannot reach %s: %v\n  Hint: check AICOMMIT_BASE_URL and your network connection", baseURL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	stop()
	if err != nil {
		fatal("Failed reading API response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(raw))
		var apiErr struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &apiErr) == nil && apiErr.Error.Message != "" {
			msg = apiErr.Error.Message
		}
		if h := apiHint(resp.StatusCode); h != "" {
			msg += "\n  Hint: " + h
		}
		fatal("API returned HTTP %d: %s", resp.StatusCode, msg)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Choices) == 0 {
		fatal("Unexpected API response: %s", raw)
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(out.Choices[0].Message.Content), "\n") {
		if !strings.HasPrefix(l, "```") { // strip stray code fences
			lines = append(lines, l)
		}
	}
	msg := strings.TrimSpace(strings.Join(lines, "\n"))
	if msg == "" {
		fatal("The model returned an empty commit message (try again or use another model)")
	}
	info("Done")
	return msg
}

func main() {
	commit := flag.Bool("c", false, "commit with the generated message (always asks for confirmation first)")
	rulesPath := flag.String("r", "", "path to rules file (default: <repo root>/.git-commit-rules)")
	model := flag.String("m", "", "model name")
	baseURL := flag.String("base-url", "", "API base URL")
	apiKey := flag.String("api-key", "", "API key")
	maxDiff := flag.Int("max-diff", 0, "max diff characters sent to the LLM (default: AICOMMIT_MAX_DIFF_CHARS or 60000)")
	hint := flag.String("H", "", "extra context for the LLM (e.g. ticket id, intent)")
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, "Usage: aicommit [options]\n\nEnv: AICOMMIT_BASE_URL, AICOMMIT_API_KEY, AICOMMIT_MODEL, AICOMMIT_MAX_DIFF_CHARS (fallback: OPENAI_BASE_URL, OPENAI_API_KEY)\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	repoRoot := strings.TrimSpace(mustGit("rev-parse", "--show-toplevel"))
	rules := loadRules(*rulesPath, repoRoot)

	diff := mustGit("diff", "--cached")
	if strings.TrimSpace(diff) == "" {
		fatal("Nothing to commit: no staged changes (stage with `git add`).")
	}
	files := strings.Fields(mustGit("diff", "--cached", "--name-only"))
	info("Found %d staged file(s)", len(files))
	limit := resolveMaxDiff(*maxDiff)
	if len(diff) > limit {
		info("Diff is large (%d chars), truncating to %d", len(diff), limit)
		diff = diff[:limit] + "\n[... diff truncated ...]"
	}

	branch := "(detached HEAD)"
	if b, err := git("branch", "--show-current"); err == nil && strings.TrimSpace(b) != "" {
		branch = strings.TrimSpace(b)
	}
	user := fmt.Sprintf("Current branch: %s\n\nStaged changes:\n\n%s", branch, diff)
	if *hint != "" {
		user = "Extra context from the author: " + *hint + "\n\n" + user
	}

	generate := func() string {
		return chat(
			firstNonEmpty(*baseURL, env("AICOMMIT_BASE_URL", "OPENAI_BASE_URL"), "https://api.openai.com/v1"),
			firstNonEmpty(*apiKey, env("AICOMMIT_API_KEY", "OPENAI_API_KEY")),
			firstNonEmpty(*model, env("AICOMMIT_MODEL"), "gpt-4o-mini"),
			fmt.Sprintf(systemPrompt, rules),
			user,
		)
	}

	msg := generate()
	if !*commit {
		fmt.Println(msg)
		return
	}

	// Never commit without explicit confirmation from the user.
	in := bufio.NewReader(os.Stdin)
	for {
		fmt.Printf("\n%s\n\nUse this commit message? [y]es / [e]dit / [r]egenerate / [n]o: ", msg)
		answer, err := in.ReadString('\n')
		if err != nil { // no input available (EOF / non-interactive): abort safely
			fmt.Fprintln(os.Stderr)
			fatal("No confirmation received, aborting without committing.")
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "y", "yes":
			runCommit("-m", msg)
			return
		case "e", "edit":
			runCommit("-e", "-m", msg)
			return
		case "r", "regenerate":
			msg = generate()
		case "n", "no":
			fmt.Println("Aborted, nothing committed.")
			return
		}
	}
}

func runCommit(args ...string) {
	cmd := exec.Command("git", append([]string{"commit"}, args...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	info("Committing...")
	if err := cmd.Run(); err != nil {
		fatal("git commit failed: %v", err)
	}
	info("Committed successfully")
}
