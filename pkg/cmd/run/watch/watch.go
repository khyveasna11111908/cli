package watch

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/cli/cli/api"
	"github.com/cli/cli/internal/ghrepo"
	"github.com/cli/cli/pkg/cmd/run/shared"
	"github.com/cli/cli/pkg/cmdutil"
	"github.com/cli/cli/pkg/iostreams"
	"github.com/cli/cli/utils"
	"github.com/spf13/cobra"
)

type WatchOptions struct {
	IO         *iostreams.IOStreams
	HttpClient func() (*http.Client, error)
	BaseRepo   func() (ghrepo.Interface, error)

	RunID    string
	Interval int

	Prompt bool

	Now func() time.Time
}

func NewCmdWatch(f *cmdutil.Factory, runF func(*WatchOptions) error) *cobra.Command {
	opts := &WatchOptions{
		IO:         f.IOStreams,
		HttpClient: f.HttpClient,
		Now:        time.Now,
		// TODO allow setting via flag?
		Interval: 2,
	}

	cmd := &cobra.Command{
		Use:    "watch <run-selector>",
		Short:  "Runs until a run completes, showing its progress",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// support `-R, --repo` override
			opts.BaseRepo = f.BaseRepo

			if len(args) > 0 {
				opts.RunID = args[0]
			} else if !opts.IO.CanPrompt() {
				return &cmdutil.FlagError{Err: errors.New("run ID required when not running interactively")}
			} else {
				opts.Prompt = true
			}

			if runF != nil {
				return runF(opts)
			}

			return watchRun(opts)
		},
	}

	return cmd
}

func watchRun(opts *WatchOptions) error {
	c, err := opts.HttpClient()
	if err != nil {
		return fmt.Errorf("failed to create http client: %w", err)
	}
	client := api.NewClientFromHTTP(c)

	repo, err := opts.BaseRepo()
	if err != nil {
		return fmt.Errorf("failed to determine base repo: %w", err)
	}

	runID := opts.RunID

	if opts.Prompt {
		cs := opts.IO.ColorScheme()
		runID, err = shared.PromptForRun(cs, client, repo)
		if err != nil {
			return err
		}
	}

	// TODO filter by non-completed
	run, err := shared.GetRun(client, repo, runID)
	if err != nil {
		return fmt.Errorf("failed to get run: %w", err)
	}

	prNumber := ""
	number, err := shared.PullRequestForRun(client, repo, *run)
	if err == nil {
		prNumber = fmt.Sprintf(" #%d", number)
	}

	// clear entire screen
	fmt.Fprint(opts.IO.Out, "\033[2J")

	for run.Status != shared.Completed {
		run, err = renderRun(*opts, client, repo, run, prNumber)
		if err != nil {
			return err
		}
		time.Sleep(time.Duration(opts.Interval * 1000))
	}

	return nil
}

func renderRun(opts WatchOptions, client *api.Client, repo ghrepo.Interface, run *shared.Run, prNumber string) (*shared.Run, error) {
	out := opts.IO.Out
	cs := opts.IO.ColorScheme()

	var err error

	run, err = shared.GetRun(client, repo, fmt.Sprintf("%d", run.ID))
	if err != nil {
		return run, fmt.Errorf("failed to get run: %w", err)
	}

	ago := opts.Now().Sub(run.CreatedAt)

	jobs, err := shared.GetJobs(client, repo, *run)
	if err != nil {
		return run, fmt.Errorf("failed to get jobs: %w", err)
	}

	var annotations []shared.Annotation

	var annotationErr error
	var as []shared.Annotation
	for _, job := range jobs {
		as, annotationErr = shared.GetAnnotations(client, repo, job)
		if annotationErr != nil {
			break
		}
		annotations = append(annotations, as...)
	}

	if annotationErr != nil {
		return run, fmt.Errorf("failed to get annotations: %w", annotationErr)
	}

	// Move cursor to 0,0
	fmt.Fprint(opts.IO.Out, "\033[0;0H")
	// Clear from cursor to bottom of screen
	fmt.Fprint(opts.IO.Out, "\033[J")

	fmt.Fprintln(out, cs.Boldf("Refreshing run status every %d seconds. Press Ctrl+C to quit.", opts.Interval))
	fmt.Fprintln(out)
	fmt.Fprintln(out, shared.RenderRunHeader(cs, *run, utils.FuzzyAgo(ago), prNumber))
	fmt.Fprintln(out)

	if len(jobs) == 0 && run.Conclusion == shared.Failure {
		// TODO are we supporting exit status here?
		return run, nil
	}

	fmt.Fprintln(out, cs.Bold("JOBS"))

	fmt.Fprintln(out, shared.RenderJobs(cs, jobs, true))

	if len(annotations) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, cs.Bold("ANNOTATIONS"))
		fmt.Fprintln(out, shared.RenderAnnotations(cs, annotations))
	}

	// TODO supporting exit status?
	//if opts.ExitStatus && shared.IsFailureState(run.Conclusion) {
	//	return cmdutil.SilentError
	//}

	return run, nil
}
