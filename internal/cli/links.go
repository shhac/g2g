package cli

import (
	"fmt"
	"strings"
)

// Linking has two halves, deliberately kept apart.
//
// Presentation.hyperlink is the capability: may this output carry a link at
// all. This file is the policy: what does a given thing point at, and when two
// services could both answer, which one wins.
//
// Keeping them apart is what makes the feature extensible in either direction.
// A new destination is a resolver added to one ordered list. A new linkable
// thing is a new subject type and its own list. Neither requires touching a
// render site, and no render site ever builds a URL — which is the property
// that stops the preference order from being re-decided, differently, in three
// places.

// pullRequestRef is everything known about one pull request that could point
// somewhere. Fields may be absent: the whole point of an ordered resolver list
// is that a partially known reference still produces the best link available.
type pullRequestRef struct {
	Number int
	// URL is the pull request's own address, as GitHub reported it.
	URL string
	// Repository is "owner/name". Services that build a URL from parts need
	// it; GitHub does not, because it already told us the address.
	Repository string
}

// pullRequestResolver returns a URL for a reference, or "" when it cannot
// answer from what it was given.
type pullRequestResolver func(pullRequestRef) string

// pullRequestResolvers is the preference order, declared once.
//
// GitHub first because it is where the pull request actually lives, and
// because its address is reported rather than assembled — a link that came
// back from the API cannot be wrong about the repository the way a constructed
// one can. Graphite answers only when GitHub did not.
var pullRequestResolvers = []pullRequestResolver{gitHubPullRequest, graphitePullRequest}

// gitHubPullRequest uses the address GitHub gave us.
func gitHubPullRequest(ref pullRequestRef) string { return ref.URL }

// graphitePullRequest builds Graphite's view of a GitHub pull request.
//
// Graphite is optional here in the same way it is optional everywhere else in
// this tool: a repository that does not use it simply never produces one of
// these, and nothing degrades.
func graphitePullRequest(ref pullRequestRef) string {
	if ref.Number <= 0 || !strings.Contains(ref.Repository, "/") {
		return ""
	}
	return fmt.Sprintf("https://app.graphite.com/github/pr/%s/%d", ref.Repository, ref.Number)
}

// pullRequestURL is where a pull request reference points, or "" if nowhere.
func pullRequestURL(ref pullRequestRef) string {
	for _, resolve := range pullRequestResolvers {
		if url := resolve(ref); url != "" {
			return url
		}
	}
	return ""
}

// repositoryFromPullRequestURL reads "owner/name" out of a pull request
// address, so a view that has one link can build the rest.
//
// This exists so the repository does not have to be threaded through every
// plan and view for the sake of a fallback. GitHub already tells us the
// repository in every URL it returns; reading it back is exact, and it is
// empty precisely when there was no URL to read.
func repositoryFromPullRequestURL(url string) string {
	_, path, found := strings.Cut(url, "://")
	if !found {
		return ""
	}
	segments := strings.Split(path, "/")
	// host / owner / name / "pull" / number
	if len(segments) < 5 || segments[3] != "pull" {
		return ""
	}
	return segments[1] + "/" + segments[2]
}
