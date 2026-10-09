package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/comment"
	localgit "github.com/shhac/g2g/internal/git"
	"github.com/shhac/g2g/internal/push"
	"github.com/shhac/g2g/internal/shape"
	"github.com/shhac/g2g/internal/stack"
	"github.com/shhac/g2g/internal/submit"
)

func newSubmit(service submit.Service, comments comment.Service, completions stack.Completions, guard func(context.Context) error, presentation Presentation) *cobra.Command {
	options := submitOptions{guard: guard, comments: comments}
	cmd := &cobra.Command{Use: "submit", GroupID: groupPublish, Short: "Publish a stack and create missing draft PRs (preview by default)", Args: cobra.NoArgs}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if err := options.selection.validate(); err != nil {
			return err
		}
		if err := options.validate(); err != nil {
			return err
		}
		return options.run(cmd, strictSubmit(service, options.strict), presentation.resolve(cmd))
	}
	options.selection.register(cmd, completions, stack.ReadableSources, "local branch to submit (defaults to current branch)", "trunk to use as the submit base")
	// A GitHub native stack is linear, so these are the two scopes that can
	// produce one. stack still refuses when it forks, naming the remedy.
	options.selection.registerScope(cmd, shape.ProjectScopes, shape.ScopeStack, scopeUsage("submit", shape.ProjectScopes))
	cmd.Flags().StringVar(&options.remote, "remote", localgit.DefaultRemote, "Git remote to push to")
	cmd.Flags().StringVar(&options.specPath, "spec", "", "submission JSON spec to validate or apply")
	cmd.Flags().StringVar(&options.writeSpec, "write-spec", "", "write a draft spec in a private temporary directory, without applying")
	cmd.Flags().BoolVar(&options.edit, "edit", false, "create and edit one temporary submission spec document")
	cmd.Flags().BoolVar(&options.keepSpec, "keep-spec", false, "keep the temporary --edit spec after a successful apply")
	cmd.Flags().StringVar(&options.template, "template", "", "repository pull request template name to prefill generated specs")
	cmd.Flags().BoolVar(&options.noTemplate, "no-template", false, "do not prefill bodies from a repository template")
	// Draft is the default and has no flag of its own. Opening a pull request
	// ready for review notifies reviewers and cannot be taken back, so it is
	// the thing a person opts into; there is nothing to opt into about a draft.
	// --no-ready exists only to overrule a spec that asked for ready.
	cmd.Flags().BoolVar(&options.ready, "ready", false, "create missing pull requests ready for review instead of as drafts")
	cmd.Flags().BoolVar(&options.noReady, "no-ready", false, "create missing pull requests as drafts even if the spec asks for ready")
	cmd.MarkFlagsMutuallyExclusive("ready", "no-ready")
	cmd.MarkFlagsMutuallyExclusive("edit", "spec")
	cmd.MarkFlagsMutuallyExclusive("edit", "write-spec")
	cmd.Flags().BoolVar(&options.apply, "apply", false, "atomically push and create missing PRs after revalidation")
	// Opt-in because GitHub will not merge a linked pull request through gh pr
	// merge, which is how g2g land merges each one.
	cmd.Flags().BoolVar(&options.link, "link", false, "also link the pull requests as a GitHub native stack (g2g land refuses a linked stack)")
	cmd.Flags().BoolVar(&options.noComment, "no-comment", false, "do not keep the stack comment on each pull request afterwards")
	registerNoSetUpstream(cmd, &options.noSetUpstream)
	registerStrict(cmd, &options.strict)
	return cmd
}

type submitOptions struct {
	// comments keeps the stack comment on each pull request once the stack
	// is published, unless noComment.
	comments  comment.Service
	noComment bool
	link      bool
	// noSetUpstream leaves each pushed branch's upstream as it was.
	noSetUpstream bool
	// strict refuses the publish if anything is out of step, as push's does.
	strict bool
	// guard refuses the command while another operation has left the
	// repository part-way through a rewrite.
	guard      func(context.Context) error
	selection  stackOptions
	remote     string
	specPath   string
	writeSpec  string
	template   string
	apply      bool
	ready      bool
	noReady    bool
	noTemplate bool
	edit       bool
	keepSpec   bool
}

// validate refuses flags that would be silently ignored. A template prefills
// the bodies of a spec being created; a spec given with --spec already has its
// bodies, and a preview naming the template it would not use is a preview of
// something else. --keep-spec keeps the document --edit creates, and there is
// no such document without it.
func (o submitOptions) validate() error {
	if o.specPath != "" && (o.template != "" || o.noTemplate) {
		return fmt.Errorf("--template and --no-template choose how a new spec is prefilled, and --spec %s already carries its bodies · drop the template flag, or use --write-spec or --edit", o.specPath)
	}
	if o.keepSpec && !o.edit {
		return fmt.Errorf("--keep-spec keeps the document --edit creates · add --edit, or drop --keep-spec")
	}
	return nil
}

// keepsComments reports whether this run keeps the stack comments afterwards.
func (o submitOptions) keepsComments() bool { return !o.noComment && o.comments.Ready() }

func (o *submitOptions) run(cmd *cobra.Command, service submit.Service, presentation Presentation) error {
	budgets := newBudgets(cmd)
	root := commandContext(cmd.Context(), cmd, applyMode(o.apply), o.selection.branch, o.selection.trunk)
	ctx, cancel := budgets.discovery(root)
	defer cancel()
	plan, err := service.Plan(ctx, o.selection.Selection(), o.remote, upstreamFor(o.noSetUpstream))
	if err != nil {
		return err
	}
	chosenTemplate, templateName, err := resolveTemplate(o.template, o.noTemplate)
	if err != nil {
		return err
	}
	if o.writeSpec != "" {
		return o.writeDraft(cmd, plan, chosenTemplate, resolveDraft(submit.DefaultDraft, o.ready, o.noReady), presentation)
	}
	// --edit creates the document this command then reads, and from here on
	// it is read exactly as a --spec given on the command line would be.
	if o.edit {
		if o.specPath, err = o.editedSpec(ctx, cmd, plan, chosenTemplate); err != nil {
			return err
		}
	}
	chosen, err := o.submission(plan)
	if err != nil {
		return err
	}
	return o.flow(cmd, service, plan, chosen.spec, presentation, templateName, chosen.invitation).run(cmd, root, budgets, presentation, chosen.apply)
}

// submission is what a run previews or applies: the spec, the line closing a
// preview that could apply, and whether this is an apply.
type submission struct {
	spec       submit.Spec
	invitation string
	apply      bool
}

// submission decides what to submit. A spec is read when there is one. With
// none, pull requests that all exist need no spec, a blocked apply is let
// through to say why it refuses, an apply that would create pull requests
// fails naming the command that writes a spec, and anything else is a preview
// whatever was asked, because there is nothing to apply.
func (o submitOptions) submission(plan submit.Plan) (submission, error) {
	if o.specPath != "" {
		spec, err := submit.Read(o.specPath, plan.Snapshot.Branches)
		if err != nil {
			return submission{}, o.actionableSpecError(err, o.specPath)
		}
		spec.Draft = resolveDraft(spec.Draft, o.ready, o.noReady)
		return submission{spec, "Rerun with --apply" + readyFlag(spec.Draft) + linkFlag(o.link) + " to push and create missing PRs.", o.apply}, nil
	}
	if spec, existingOnly := plan.ExistingSpec(); existingOnly {
		return submission{spec, "Rerun with --apply to publish commits to the existing PRs.", o.apply}, nil
	}
	if o.apply {
		if plan.Blocked() != "" {
			return submission{spec: submit.Spec{Draft: submit.DefaultDraft}, apply: true}, nil
		}
		return submission{}, fmt.Errorf("missing PRs require a submission spec · create one with %s, fill in the titles, then validate and apply with --spec", o.retryCommand("--write-spec", "<private-temp-dir>"))
	}
	// No spec has been read, so the choice is whatever the flags say over the
	// default a fresh spec would carry.
	draft := resolveDraft(submit.DefaultDraft, o.ready, o.noReady)
	return submission{submit.Spec{Draft: draft}, "Create a spec with: " + runnable(o.retryCommand("--write-spec", "<private-temp-dir>")+readyFlag(draft)), false}, nil
}

// flow is submit's preview and apply. The spec is what a preview is of and
// what an apply publishes; invitation closes a preview that could apply.
func (o submitOptions) flow(cmd *cobra.Command, service submit.Service, preview submit.Plan, spec submit.Spec, p Presentation, template, invitation string) applyFlow[submit.Plan] {
	applied := "Applied — stack published and missing pull requests created"
	if _, existingOnly := preview.ExistingSpec(); existingOnly {
		applied = "Applied — stack published to existing pull requests"
	}
	retry := o.retryCommand("--apply")
	if o.specPath != "" {
		retry = o.retryCommand("--spec", o.specPath, "--apply")
	}
	flow := applyFlow[submit.Plan]{
		// The preview is already in hand, so the flow starts from it; an apply
		// still re-discovers through plan before mutating.
		discovered: &preview,
		plan: func(ctx context.Context) (submit.Plan, error) {
			return service.Plan(ctx, o.selection.Selection(), o.remote, upstreamFor(o.noSetUpstream))
		},
		revalidation: revalidation{"submit", "submit plan"},
		precheck:     service.RequireClean,
		suggest:      func(plan submit.Plan) string { return githubStatusNext(plan.Snapshot) },
		blocked: func(plan submit.Plan) string {
			if blocked := submitBlocked(plan); blocked != "" {
				return blocked
			}
			if o.link {
				return plan.LinkBlocked()
			}
			return ""
		},
		render: func(w io.Writer, plan submit.Plan, presentation Presentation) error {
			return writeSubmitPreview(w, plan, presentation, template, spec.Draft, o.link, o.keepsComments())
		},

		execute: func(ctx context.Context, plan submit.Plan) error {
			if err := service.Apply(ctx, plan, spec, o.link); err != nil {
				return err
			}
			if o.edit && !o.keepSpec {
				_ = os.RemoveAll(filepath.Dir(o.specPath))
			}
			selection := o.selection.Selection()
			selection.Branch = plan.Snapshot.Target
			return keepComments(ctx, o.comments, o.keepsComments(), selection)
		},
		// The pull requests exist, and are linked if asked, whatever happens
		// to their comments, so a failure there is not the submission failing.
		// A failure after the push, or after a pull request opened, leaves
		// those standing, so it is not "not applied" either.
		interrupted: func(_ context.Context, plan submit.Plan, err error) (bool, error) {
			comments := selected{branch: plan.Snapshot.Target, named: plan.Snapshot.TargetSource == shape.TargetNamed}.aimedOr(commentCommand) + " --apply"
			if handled, report := commentsNotKept(cmd, err, comments, p); handled {
				return true, report
			}
			return claim(err, func(stopped *submit.Stopped) error { return stoppedMidSubmit(cmd, stopped, o.remote, retry, p) })
		},
		branches: func(plan submit.Plan) int { return len(plan.Snapshot.Branches) },
		wrapMutationError: func(err error) error {
			if o.specPath == "" {
				return err
			}
			return fmt.Errorf("submission spec retained at %s: %w", o.specPath, err)
		},
		notices: flowNotices{
			preview:  invitation,
			applied:  applied,
			changed:  "Changes were made.",
			recovery: fmt.Sprintf("Rerunning %s is safe: it preserves existing pull requests and creates only the missing ones.", retry),
		},
	}
	// Only an apply is refused mid-restack. A preview changes nothing, and
	// submit's has always been readable while a rewrite is part-way.
	if o.apply {
		flow.guard = o.guard
	}
	return flow
}

// submitBlocked is why a submission must not be applied, in the words an
// apply that refuses says it.
func submitBlocked(plan submit.Plan) string {
	switch {
	case len(plan.Issues) != 0:
		return "submit is blocked by " + plan.Blocked()
	case plan.Push.Blocked() != "":
		return "submit cannot publish: " + plan.Push.Blocked()
	}
	return ""
}

// stoppedMidSubmit says what a submission published before it stopped. It
// makes no call of its own: the mutation budget may be what ran out.
func stoppedMidSubmit(cmd *cobra.Command, stopped *submit.Stopped, remote, retry string, p Presentation) error {
	done := ""
	if stopped.Pushed {
		done = "Published the stack to " + remote + ". "
	}
	if len(stopped.Opened) != 0 {
		done += "Opened " + pick(len(stopped.Opened), "a pull request", "pull requests") + " for " + branchList(stopped.Opened) + ". "
	}
	return writeStoppedPartWay(cmd, p,
		"Stopped part-way: "+stopped.Err.Error(),
		done+"That stands. Rerun "+runnable(retry)+" to finish; it keeps the pull requests that exist and opens only the missing ones.",
		stopped)
}

// strictSubmit is submit publishing through a strict push. submit owns no rule
// about what may be published; it is push's, so the flag is push's too.
func strictSubmit(service submit.Service, strict bool) submit.Service {
	pusher, ok := service.Pusher.(*push.Service)
	if !strict || !ok {
		return service
	}
	strictPush := *pusher
	strictPush.Strict = true
	service.Pusher = &strictPush
	return service
}
