// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// mainHeadStepName is the tag-only step that refuses a tag whose commit is not
// the protected main head.
const mainHeadStepName = "Require the tag to point at the protected main head"

// releaseStep holds the step fields the release workflow tests read.
type releaseStep struct {
	Name string            `yaml:"name"`
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	If   string            `yaml:"if"`
	With map[string]any    `yaml:"with"`
	Env  map[string]string `yaml:"env"`
}

// releaseJob holds the job fields the release workflow tests read.
type releaseJob struct {
	Env   map[string]string `yaml:"env"`
	Steps []releaseStep     `yaml:"steps"`
}

// releaseStepIndex returns the position of the first step for which match is
// true, or -1.
func releaseStepIndex(steps []releaseStep, match func(releaseStep) bool) int {
	for i, step := range steps {
		if match(step) {
			return i
		}
	}
	return -1
}

// parseReleaseImageJob parses raw and returns its image job.
func parseReleaseImageJob(raw []byte) (releaseJob, error) {
	var workflow struct {
		Jobs map[string]releaseJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		return releaseJob{}, err
	}
	job, ok := workflow.Jobs["image"]
	if !ok {
		return releaseJob{}, fmt.Errorf("release.yml has no image job")
	}
	return job, nil
}

// readReleaseImageJob loads the checked-in release workflow's image job.
func readReleaseImageJob(t *testing.T) releaseJob {
	t.Helper()
	raw, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	job, err := parseReleaseImageJob(raw)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

// mainHeadProblems checks the guard that refuses a v* tag whose commit is not
// the protected main head: a tag-only step that runs after a credential-free
// checkout, compares github.sha with a freshly fetched origin/main, and
// precedes the manifest check and the image push.
func mainHeadProblems(job releaseJob) []string {
	steps := job.Steps
	guardIdx := releaseStepIndex(steps, func(s releaseStep) bool { return s.Name == mainHeadStepName })
	if guardIdx < 0 {
		return []string{fmt.Sprintf("no step named %q", mainHeadStepName)}
	}
	var problems []string
	guard := steps[guardIdx]
	if guard.If != "github.ref_type == 'tag'" {
		problems = append(problems, fmt.Sprintf("step %q runs when %q, want only tag events", mainHeadStepName, guard.If))
	}
	if got := guard.Env["TAG_COMMIT"]; got != "${{ github.sha }}" {
		problems = append(problems, fmt.Sprintf("step %q TAG_COMMIT = %q, want ${{ github.sha }}", mainHeadStepName, got))
	}
	for _, want := range []string{"git fetch origin main", "git rev-parse origin/main", `"${TAG_COMMIT}" != "${main_head}"`, "exit 1"} {
		if !strings.Contains(guard.Run, want) {
			problems = append(problems, fmt.Sprintf("step %q lacks %q", mainHeadStepName, want))
		}
	}
	if got := job.Env["TAG_NAME"]; got != "${{ github.ref_name }}" {
		problems = append(problems, fmt.Sprintf("job env TAG_NAME = %q, want ${{ github.ref_name }}", got))
	}
	checkoutIdx := releaseStepIndex(steps, func(s releaseStep) bool { return strings.HasPrefix(s.Uses, "actions/checkout@") })
	switch {
	case checkoutIdx < 0:
		problems = append(problems, "no checkout step before the main-head guard")
	case checkoutIdx > guardIdx:
		problems = append(problems, "the main-head guard runs before the checkout")
	default:
		if got := fmt.Sprint(steps[checkoutIdx].With["persist-credentials"]); got != "false" {
			problems = append(problems, fmt.Sprintf("checkout persist-credentials = %q, want false", got))
		}
	}
	versionIdx := releaseStepIndex(steps, func(s releaseStep) bool { return s.Name == "Require the tag to match the manifest version" })
	if versionIdx >= 0 && guardIdx > versionIdx {
		problems = append(problems, "the main-head guard runs after the manifest version check")
	}
	publishIdx := releaseStepIndex(steps, func(s releaseStep) bool { return fmt.Sprint(s.With["push"]) == "true" })
	if publishIdx < 0 {
		problems = append(problems, "no step pushes the image")
	} else if guardIdx > publishIdx {
		problems = append(problems, "the main-head guard runs after the image is pushed")
	}
	return problems
}

// TestReleaseWorkflowRefusesTagsOffTheMainHead proves a pushed v* tag is
// refused unless it points at the protected main head, before anything builds.
func TestReleaseWorkflowRefusesTagsOffTheMainHead(t *testing.T) {
	t.Parallel()

	if problems := mainHeadProblems(readReleaseImageJob(t)); len(problems) > 0 {
		t.Fatalf("main-head guard:\n%s", strings.Join(problems, "\n"))
	}
}

// TestReleaseMainHeadGuardRejectsViolations plants each way the guard can be
// weakened in a copy of the workflow, so a gate that always passes is caught.
func TestReleaseMainHeadGuardRejectsViolations(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	good := string(raw)
	for name, mutate := range map[string]func(string) string{
		"guard renamed": func(s string) string { return strings.Replace(s, mainHeadStepName, "Check the tag", 1) },
		"guard runs on every event": func(s string) string {
			return strings.Replace(s, "      - name: "+mainHeadStepName+"\n        if: github.ref_type == 'tag'\n", "      - name: "+mainHeadStepName+"\n", 1)
		},
		"guard compares the wrong commit": func(s string) string {
			return strings.Replace(s, "TAG_COMMIT: ${{ github.sha }}", "TAG_COMMIT: ${{ github.ref }}", 1)
		},
		"guard does not fetch main": func(s string) string {
			return strings.Replace(s, "          git fetch origin main --quiet\n", "", 1)
		},
		"guard never fails": func(s string) string {
			return strings.Replace(s, `not the protected main head ${main_head}" >&2
            exit 1`, `not the protected main head ${main_head}" >&2`, 1)
		},
		"job env drops the tag name": func(s string) string {
			return strings.Replace(s, "      TAG_NAME: ${{ github.ref_name }}\n", "", 1)
		},
		"checkout keeps credentials": func(s string) string {
			return strings.Replace(s, "persist-credentials: false", "persist-credentials: true", 1)
		},
		"guard runs after the manifest check": func(s string) string {
			start := strings.Index(s, "      - name: "+mainHeadStepName)
			end := strings.Index(s, "      - name: Require the tag to match the manifest version")
			next := strings.Index(s, "      - name: Set up Docker Buildx")
			if start < 0 || end < start || next < end {
				return s
			}
			return s[:start] + s[end:next] + s[start:end] + s[next:]
		},
	} {
		mutated := mutate(good)
		if mutated == good {
			t.Errorf("%s: the mutation did not change the workflow", name)
			continue
		}
		job, err := parseReleaseImageJob([]byte(mutated))
		if err != nil {
			t.Errorf("%s: mutated workflow does not parse: %v", name, err)
			continue
		}
		if len(mainHeadProblems(job)) == 0 {
			t.Errorf("%s: the gate accepted a violating workflow", name)
		}
	}
}

// gitIn runs git in dir and returns its trimmed stdout.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestReleaseMainHeadStepComparesTagCommitWithMain runs the workflow's own
// script the way the runner does (bash -e) in a clone of a scratch origin, so
// a tag cut from any commit other than the main head cannot publish.
func TestReleaseMainHeadStepComparesTagCommitWithMain(t *testing.T) {
	t.Parallel()

	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not available")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	var script string
	for _, step := range readReleaseImageJob(t).Steps {
		if step.Name == mainHeadStepName {
			script = step.Run
		}
	}
	if script == "" {
		t.Fatalf("release.yml has no step named %q", mainHeadStepName)
	}

	origin := t.TempDir()
	gitIn(t, origin, "init", "--quiet", "--initial-branch=main")
	gitIn(t, origin, "commit", "--quiet", "--allow-empty", "-m", "first")
	older := gitIn(t, origin, "rev-parse", "HEAD")
	gitIn(t, origin, "commit", "--quiet", "--allow-empty", "-m", "second")
	head := gitIn(t, origin, "rev-parse", "HEAD")
	gitIn(t, origin, "checkout", "--quiet", "-b", "feature")
	gitIn(t, origin, "commit", "--quiet", "--allow-empty", "-m", "unmerged")
	unmerged := gitIn(t, origin, "rev-parse", "HEAD")

	for name, tc := range map[string]struct {
		commit string
		want   bool
	}{
		"tag at the main head":        {head, true},
		"tag at an older main commit": {older, false},
		"tag at an unmerged commit":   {unmerged, false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			clone := t.TempDir()
			gitIn(t, clone, "clone", "--quiet", origin, ".")
			cmd := exec.Command(bash, "-e", "-c", script)
			cmd.Dir = clone
			cmd.Env = append(os.Environ(), "TAG_NAME=v9.9.9", "TAG_COMMIT="+tc.commit)
			out, err := cmd.CombinedOutput()
			if got := err == nil; got != tc.want {
				t.Fatalf("commit %s: passed = %t, want %t\n%s", tc.commit, got, tc.want, out)
			}
			if !tc.want && !strings.Contains(string(out), "not the protected main head") {
				t.Fatalf("refusal lacks the explanatory message:\n%s", out)
			}
		})
	}
}
