package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/hewitt-iso/hewitt/agent"
	"github.com/hewitt-iso/hewitt/config"
	"github.com/hewitt-iso/hewitt/session"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

// batchTask is a task file: its name names the branch, its front matter may
// choose the agent, and its body is the prompt.
type batchTask struct {
	name, agent, prompt, file string
}

func newBatchCommand(cfg *config.Config) *cobra.Command {
	var agentName, name, profileID, repository, file, isolation, image string
	var jobs int
	cmd := &cobra.Command{
		Use:   "batch [task files or directories]",
		Short: "Run task files headless in parallel, each agent on a branch named after its task",
		Long: "Run task files headless in parallel in the project's workspace. Each task is a Markdown file:\n" +
			"its body is the prompt, its file name names the branch, and optional front matter chooses the agent:\n\n" +
			"  ---\n  agent: codex\n  ---\n  Fix the flaky test in api/ and commit the fix.\n\n" +
			"Without arguments, batch runs tasks/*.md. Agents run as with hewitt run <agent> --headless: they cannot\n" +
			"ask for permissions, and suspicious requests appear in this terminal's tmux pane.\n" +
			"hewitt tasks shows the batch, and hewitt sync imports its branches.",
		Example: "  hewitt batch\n  hewitt batch tasks/fix-*.md --agent codex -j 8\n  hewitt tasks",
		RunE: func(cmd *cobra.Command, args []string) error {
			if jobs < 1 {
				return fmt.Errorf("--jobs must be at least 1")
			}
			if agentName != "" && !slices.Contains(agent.TaskAgents, agentName) {
				return fmt.Errorf("--agent must be one of %s", strings.Join(agent.TaskAgents, ", "))
			}
			tasks, err := readTasks(args, agentName)
			if err != nil {
				return err
			}
			opts := session.AgentOptions{DataDir: cfg.DataDir, Repository: repository, Name: name, File: file, Isolation: isolation, Image: image, Headless: true}
			if cmd.Flags().Changed("profile") {
				opts.Profile = profileID
			}
			// The project's agent applies to tasks that name none.
			project, err := trustAndRetry(cmd, cfg.DataDir, func() (string, error) { return session.DefaultAgent(opts) })
			if err != nil {
				return err
			}
			for i := range tasks {
				if tasks[i].agent == "" {
					tasks[i].agent = project
				}
				if !slices.Contains(agent.TaskAgents, tasks[i].agent) {
					if tasks[i].agent == "workspace" {
						return fmt.Errorf("task %s names no agent: add agent: to its front matter, pass --agent, or set agent in hewitt.yml", tasks[i].file)
					}
					return fmt.Errorf("task %s: %s does not accept a task (tasks work with %s)", tasks[i].file, tasks[i].agent, strings.Join(agent.TaskAgents, ", "))
				}
			}
			opts.Agent, opts.Prompt = tasks[0].agent, tasks[0].prompt
			id, err := session.RunAgent(cmd.Context(), opts, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			store, err := session.NewStore(cfg.DataDir)
			if err != nil {
				return err
			}
			m, err := store.Get(id)
			if err != nil {
				return err
			}
			return runBatch(cmd, store, m, opts, tasks, jobs)
		},
	}
	cmd.Flags().StringVar(&agentName, "agent", "", "agent for tasks whose front matter names none (default: hewitt.yml agent)")
	cmd.Flags().IntVarP(&jobs, "jobs", "j", 4, "how many agents run at once")
	cmd.Flags().StringVar(&name, "name", "", "workspace name (different names select separate containers)")
	cmd.Flags().StringVar(&profileID, "profile", "default", "persistent profile ID, scoped to the agent")
	cmd.Flags().StringVar(&repository, "repo", ".", "source directory or Git repository (default: current directory)")
	cmd.Flags().StringVarP(&file, "file", "f", "", "hewitt.yml path (default: hewitt.yml in --repo)")
	cmd.Flags().StringVar(&isolation, "isolation", "", "isolation level: shared, isolated, restricted, or hardened")
	cmd.Flags().StringVar(&image, "image", "", "override the default image or hewitt.yml image")
	return cmd
}

// readTasks reads task files from args, or tasks/*.md without arguments.
// Directories contribute their *.md files in name order.
func readTasks(args []string, agentName string) ([]batchTask, error) {
	if len(args) == 0 {
		args = []string{"tasks"}
	}
	var files []string
	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil {
			return nil, fmt.Errorf("read tasks: %w", err)
		}
		if !info.IsDir() {
			files = append(files, arg)
			continue
		}
		matches, err := filepath.Glob(filepath.Join(arg, "*.md"))
		if err != nil {
			return nil, fmt.Errorf("read tasks: %w", err)
		}
		files = append(files, matches...)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no task files in %s; write each task as a .md file there", strings.Join(args, ", "))
	}
	var tasks []batchTask
	seen := map[string]string{}
	for _, path := range files {
		t, err := readTaskFile(path)
		if err != nil {
			return nil, err
		}
		if other, ok := seen[t.name]; ok {
			return nil, fmt.Errorf("tasks %s and %s both name branch %s; rename one", other, path, t.name)
		}
		seen[t.name] = path
		if t.agent == "" {
			t.agent = agentName
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// readTaskFile parses one task. Front matter is YAML between --- lines at
// the start of the file; agent is its only key.
func readTaskFile(path string) (batchTask, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return batchTask{}, fmt.Errorf("read task: %w", err)
	}
	t := batchTask{file: path, name: taskName(path)}
	if err := session.ValidTaskName(t.name); err != nil {
		return batchTask{}, fmt.Errorf("task %s: %w", path, err)
	}
	body := string(data)
	if rest, ok := strings.CutPrefix(strings.ReplaceAll(body, "\r\n", "\n"), "---\n"); ok {
		front, after, found := strings.Cut(rest, "\n---\n")
		if !found {
			front, found = strings.CutSuffix(rest, "\n---")
		}
		if !found {
			return batchTask{}, fmt.Errorf("task %s: front matter has no closing ---", path)
		}
		var meta struct {
			Agent string `yaml:"agent"`
		}
		decoder := yaml.NewDecoder(strings.NewReader(front))
		decoder.KnownFields(true)
		if err := decoder.Decode(&meta); err != nil && !errors.Is(err, io.EOF) {
			return batchTask{}, fmt.Errorf("task %s: front matter: %w", path, err)
		}
		if meta.Agent != "" && !slices.Contains(agent.TaskAgents, meta.Agent) {
			return batchTask{}, fmt.Errorf("task %s: agent must be one of %s", path, strings.Join(agent.TaskAgents, ", "))
		}
		t.agent, body = meta.Agent, after
	}
	t.prompt = strings.TrimSpace(body)
	switch {
	case t.prompt == "":
		return batchTask{}, fmt.Errorf("task %s has no prompt", path)
	case len(t.prompt) > maxTask:
		return batchTask{}, fmt.Errorf("task %s is longer than %d KiB; put the details in a file in the repository and refer to it", path, maxTask>>10)
	case strings.ContainsRune(t.prompt, 0):
		return batchTask{}, fmt.Errorf("task %s contains a NUL byte", path)
	}
	return t, nil
}

// taskName turns a file name into a branch name: lowercase, with runs of other
// characters replaced by one hyphen.
func taskName(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(base) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	name := strings.TrimRight(b.String(), "-")
	if len(name) > 40 {
		name = strings.TrimRight(name[:40], "-")
	}
	return name
}

// BatchError reports tasks that did not finish.
type BatchError struct {
	Batch     string
	Failed    int
	Total     int
	Workspace string
	Canceled  bool
}

func (e *BatchError) Error() string {
	if e.Canceled {
		return fmt.Sprintf("batch %s canceled; %d of %d tasks did not finish", e.Batch, e.Failed, e.Total)
	}
	return fmt.Sprintf("%d of %d tasks in batch %s did not finish", e.Failed, e.Total, e.Batch)
}

func (e *BatchError) Hint() string {
	return "hewitt tasks " + e.Batch + " shows them and hewitt tasks " + e.Batch + " <task> prints a log; hewitt sync " + e.Workspace + " imports the finished branches"
}

func runBatch(cmd *cobra.Command, store *session.Store, m session.Metadata, opts session.AgentOptions, tasks []batchTask, jobs int) error {
	batch, err := store.NewBatch()
	if err != nil {
		return err
	}
	defer batch.Close()
	workspace := m.Name
	if workspace == "" {
		workspace = shortID(m.ID)
	}
	records := make([]session.TaskRecord, len(tasks))
	for i, t := range tasks {
		records[i] = session.TaskRecord{Name: t.name, Agent: t.agent, WorkspaceID: m.ID, Workspace: workspace, State: session.TaskQueued, Log: batch.LogPath(t.name)}
		if err := batch.Save(records[i]); err != nil {
			return err
		}
	}
	var mu sync.Mutex // guards records and progress output
	stderr := cmd.ErrOrStderr()
	report := func(i int, format string, args ...any) {
		fmt.Fprintf(stderr, "[hewitt] %-*s  %-8s  %s\n", longestName(tasks), tasks[i].name, tasks[i].agent, fmt.Sprintf(format, args...))
	}
	update := func(i int, change func(*session.TaskRecord)) {
		mu.Lock()
		defer mu.Unlock()
		change(&records[i])
		if err := batch.Save(records[i]); err != nil {
			fmt.Fprintf(stderr, "[hewitt] %v\n", err)
		}
	}
	if _, err := fmt.Fprintf(stderr, "[hewitt] Batch %s: %d tasks in workspace %s, %d at a time\n", batch.ID, len(tasks), workspace, min(jobs, len(tasks))); err != nil {
		return err
	}
	ctx := cmd.Context()
	slots := make(chan struct{}, jobs)
	var wg sync.WaitGroup
	for i, t := range tasks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				update(i, func(r *session.TaskRecord) { r.State = session.TaskCanceled })
				return
			}
			defer func() { <-slots }()
			log, err := os.OpenFile(batch.LogPath(t.name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			if err != nil {
				update(i, func(r *session.TaskRecord) { r.State, r.Exit = session.TaskFailed, 1 })
				mu.Lock()
				report(i, "failed: %v", err)
				mu.Unlock()
				return
			}
			defer log.Close()
			out := &taskLog{w: log, branch: func(branch string) {
				update(i, func(r *session.TaskRecord) { r.Branch = branch })
				mu.Lock()
				report(i, "branch %s", branch)
				mu.Unlock()
			}}
			topts := opts
			topts.Agent, topts.Prompt, topts.Task = t.agent, t.prompt, t.name
			topts.Started = func(run string) { update(i, func(r *session.TaskRecord) { r.Run = run }) }
			started := time.Now()
			update(i, func(r *session.TaskRecord) { r.State, r.Started = session.TaskRunning, started.UTC() })
			mu.Lock()
			report(i, "started")
			mu.Unlock()
			err = session.OpenWorkspace(ctx, store, m.ID, topts, strings.NewReader(""), out, out)
			took := time.Since(started).Round(time.Second)
			state, exit := session.TaskDone, 0
			var agentExit *session.AgentExitError
			switch {
			case ctx.Err() != nil:
				state = session.TaskCanceled
			case errors.As(err, &agentExit):
				state, exit = session.TaskFailed, agentExit.Code
			case err != nil:
				state, exit = session.TaskFailed, 1
				fmt.Fprintf(out, "[hewitt] %v\n", err)
			}
			update(i, func(r *session.TaskRecord) { r.State, r.Exit, r.Finished = state, exit, time.Now().UTC() })
			mu.Lock()
			defer mu.Unlock()
			switch state {
			case session.TaskDone:
				report(i, "done in %s", took)
			case session.TaskCanceled:
				report(i, "canceled after %s", took)
			default:
				report(i, "failed in %s (exit %d); log: %s", took, exit, batch.LogPath(t.name))
			}
		}()
	}
	wg.Wait()
	failed := 0
	for _, r := range records {
		if r.State != session.TaskDone {
			failed++
		}
	}
	if failed > 0 {
		return &BatchError{Batch: batch.ID, Failed: failed, Total: len(tasks), Workspace: workspace, Canceled: ctx.Err() != nil}
	}
	_, err = fmt.Fprintf(stderr, "[hewitt] Batch %s finished; hewitt sync %s imports its branches, hewitt tasks shows them\n", batch.ID, workspace)
	return err
}

func longestName(tasks []batchTask) int {
	n := 0
	for _, t := range tasks {
		n = max(n, len(t.name))
	}
	return n
}

// taskLog writes an agent's output to its log and reports the branch line
// that hewitt-agent prints when it creates the task's worktree.
type taskLog struct {
	mu     sync.Mutex
	w      io.Writer
	line   []byte
	found  bool
	branch func(string)
}

func (l *taskLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.found {
		l.line = append(l.line, p...)
		for {
			i := bytes.IndexByte(l.line, '\n')
			if i < 0 {
				break
			}
			if branch, ok := strings.CutPrefix(strings.TrimSpace(string(l.line[:i])), "[hewitt] Branch "); ok {
				l.found, l.line = true, nil
				l.branch(branch)
				break
			}
			l.line = l.line[i+1:]
		}
		if len(l.line) > 4096 {
			l.line = nil
		}
	}
	return l.w.Write(p)
}

func newTasksCommand(cfg *config.Config) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "tasks [batch [task]]",
		Short: "Show the tasks of a batch, or print one task's log",
		Long: "Show the tasks of the latest batch, or of the batch named: agent, branch, state, and time.\n" +
			"A task waiting for a suspicious request shows SRNA; hewitt requests lists those requests.\n" +
			"With a task name, print that task's log. --all lists every batch.",
		Example: "  hewitt tasks\n  hewitt tasks --all\n  hewitt tasks 20261122-153000 fix-flaky-test",
		Args:    cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := session.NewStore(cfg.DataDir)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			ids, err := store.Batches()
			if err != nil {
				return err
			}
			if all {
				return listBatches(out, store, ids)
			}
			id := ""
			if len(args) > 0 {
				id = args[0]
			} else if len(ids) > 0 {
				id = ids[len(ids)-1]
			} else {
				_, err := fmt.Fprintln(out, "No batches yet; hewitt batch runs tasks/*.md.")
				return err
			}
			tasks, live, err := store.BatchTasks(id)
			if err != nil {
				return err
			}
			if len(args) == 2 {
				for _, t := range tasks {
					if t.Name == args[1] {
						f, err := os.Open(t.Log)
						if err != nil {
							return fmt.Errorf("read log of task %s: %w", t.Name, err)
						}
						defer f.Close()
						_, err = io.Copy(out, f)
						return err
					}
				}
				return fmt.Errorf("batch %s has no task %s", id, args[1])
			}
			return showBatch(out, store, id, tasks, live)
		},
	}
	cmd.Flags().BoolVarP(&all, "all", "a", false, "list every batch")
	return cmd
}

func showBatch(out io.Writer, store *session.Store, id string, tasks []session.TaskRecord, live bool) error {
	pending := map[string]int{}
	if live {
		if requests, err := store.Requests(); err == nil {
			for _, r := range requests {
				if r.Decision == "" && r.Run != "" {
					pending[r.Run]++
				}
			}
		}
	}
	state := "finished"
	if live {
		state = "running"
	}
	workspace := ""
	if len(tasks) > 0 {
		workspace = tasks[0].Workspace
	}
	if _, err := fmt.Fprintf(out, "Batch %s in workspace %s (%s)\n", id, workspace, state); err != nil {
		return err
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "TASK\tAGENT\tBRANCH\tSTATE\tTIME")
	done := 0
	for _, t := range tasks {
		status := t.State
		switch {
		case t.State == session.TaskRunning && pending[t.Run] > 0:
			status = fmt.Sprintf("SRNA (%d)", pending[t.Run])
		case t.State == session.TaskFailed:
			status = fmt.Sprintf("failed (exit %d)", t.Exit)
		case t.State == session.TaskDone:
			done++
		}
		took := "-"
		switch {
		case !t.Finished.IsZero() && !t.Started.IsZero():
			took = t.Finished.Sub(t.Started).Round(time.Second).String()
		case !t.Started.IsZero() && t.State == session.TaskRunning:
			took = time.Since(t.Started).Round(time.Second).String()
		}
		branch := t.Branch
		if branch == "" {
			branch = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", t.Name, t.Agent, branch, status, took)
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("write task list: %w", err)
	}
	if done > 0 && workspace != "" {
		_, err := fmt.Fprintf(out, "hewitt sync %s imports the branches; hewitt tasks %s <task> prints a log.\n", workspace, id)
		return err
	}
	return nil
}

func listBatches(out io.Writer, store *session.Store, ids []string) error {
	if len(ids) == 0 {
		_, err := fmt.Fprintln(out, "No batches yet; hewitt batch runs tasks/*.md.")
		return err
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "BATCH\tWORKSPACE\tTASKS\tDONE\tFAILED\tSTATE")
	for _, id := range ids {
		tasks, live, err := store.BatchTasks(id)
		if err != nil {
			return err
		}
		done, failed, workspace := 0, 0, "-"
		for _, t := range tasks {
			workspace = t.Workspace
			switch t.State {
			case session.TaskDone:
				done++
			case session.TaskFailed, session.TaskLost, session.TaskCanceled:
				failed++
			}
		}
		state := "finished"
		if live {
			state = "running"
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%d\t%s\n", id, workspace, len(tasks), done, failed, state)
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("write batch list: %w", err)
	}
	return nil
}
