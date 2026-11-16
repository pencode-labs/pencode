package workbench

import (
	"strings"
	"testing"
)

func TestSourceControlBranchIntegration(t *testing.T) {
	for _, operation := range []string{"merge", "rebase"} {
		for _, remote := range []bool{false, true} {
			name := operation + "/local"
			ref := "refs/heads/topic"
			if remote {
				name, ref = operation+"/remote", "refs/remotes/origin/topic"
			}
			t.Run(name, func(t *testing.T) {
				r := sourceTestRepository(t)
				sourceTestWrite(t, r, "resources/main.go", "package demo\n")
				sourceTestGit(t, r, "add", "--all")
				sourceTestGit(t, r, "commit", "-m", "base")
				sourceTestGit(t, r, "switch", "-c", "topic")
				sourceTestWrite(t, r, "topic.txt", "topic\n")
				sourceTestGit(t, r, "add", "--all")
				sourceTestGit(t, r, "commit", "-m", "topic")
				topic := sourceTestGit(t, r, "rev-parse", "HEAD")
				if remote {
					sourceTestGit(t, r, "update-ref", ref, topic)
				}
				sourceTestGit(t, r, "switch", "main")
				sourceTestWrite(t, r, "current.txt", "current\n")
				sourceTestGit(t, r, "add", "--all")
				sourceTestGit(t, r, "commit", "-m", "current")
				before := sourceTestGit(t, r, "rev-parse", "HEAD")
				t.Setenv("ONIX_PENCODE_PROFILE_PROJECT", r.Root)
				t.Setenv("ONIX_PENCODE_PROFILE_FILE", "resources/main.go")
				app, m, _, state := languageInteractionFixture(t)
				editorContextClick(t, app, m, state, "status-source-control")
				sourceWait(t, app, m)
				s := m.sourceState()
				open := func(branch string) {
					t.Helper()
					editorContextClick(t, app, m, state, "sc-branches")
					editorContextClick(t, app, m, state, "sc-branch:"+branch)
					editorContextClick(t, app, m, state, "sc-branch-"+operation)
				}
				open("refs/heads/main")
				if s.Prompt != "" || s.Done != nil || !s.BranchSubmenu {
					t.Fatal("integration with the current branch should be disabled")
				}
				editorContextClick(t, app, m, state, "sc-branches")
				open(ref)
				if s.Prompt != operation || s.PromptHash != ref || s.Done != nil || s.BranchMenuOpen {
					t.Fatal("integration did not confirm the selected branch")
				}
				editorContextClick(t, app, m, state, "sc-prompt-cancel")
				if sourceTestGit(t, r, "rev-parse", "HEAD") != before {
					t.Fatal("cancel changed HEAD")
				}
				open(ref)
				editorContextClick(t, app, m, state, "sc-prompt-confirm")
				sourceWait(t, app, m)
				if s.Snapshot.Branch != "main" || sourceTestGit(t, r, "rev-parse", ref) != topic || s.Snapshot.Rebasing {
					t.Fatal("integration changed the source branch or failed to refresh")
				}
				sourceTestGit(t, r, "merge-base", "--is-ancestor", topic, "HEAD")
				parents := strings.Fields(sourceTestGit(t, r, "show", "-s", "--format=%P", "HEAD"))
				if operation == "merge" && (len(parents) != 2 || parents[0] != before || parents[1] != topic) {
					t.Fatalf("unexpected merge parents: %v", parents)
				}
				if operation == "rebase" && (len(parents) != 1 || parents[0] != topic) {
					t.Fatalf("current branch was not rebased onto the selected branch: %v", parents)
				}
				for _, path := range []string{"topic.txt", "current.txt"} {
					sourceTestGit(t, r, "cat-file", "-e", "HEAD:"+path)
				}
			})
		}
	}
}

func TestSourceControlBranchRebaseConflict(t *testing.T) {
	for _, action := range []string{"rebase-continue", "rebase-continue-empty", "abort"} {
		t.Run(action, func(t *testing.T) {
			r, path := sourceTestConflict(t)
			before := sourceTestGit(t, r, "rev-parse", "HEAD")
			incoming := sourceTestGit(t, r, "rev-parse", "incoming")
			m := &workspaceModel{projectPath: r.Root, backend: r.Backend}
			s := m.sourceState()
			defer s.stop()
			s.Snapshot = sourceTestSnapshot(t, r)
			apply := func() sourceResult {
				t.Helper()
				result := <-s.Done
				s.Done <- result
				m.pollSourceResult(s, nil)
				return result
			}
			s.Prompt, s.PromptHash = "rebase", "refs/heads/incoming"
			m.confirmSourceOperation()
			result := apply()
			if result.Err == nil || s.Notice != "" || !s.Snapshot.Rebasing || len(s.Snapshot.Groups[0]) != 1 || s.Snapshot.syncPending() {
				t.Fatalf("unexpected rebase conflict: err=%v notice=%q snapshot=%+v", result.Err, s.Notice, s.Snapshot)
			}
			if strings.HasPrefix(action, "rebase-continue") {
				content := "resolved\n"
				if action == "rebase-continue-empty" {
					content = "incoming\n"
				}
				sourceTestWrite(t, r, path, content)
				sourceTestGit(t, r, "add", "--", path)
				s.Snapshot = sourceTestSnapshot(t, r)
				// Continue through the same button used to commit merge resolutions.
				m.sourceAction("sc-create-commit")
			} else {
				s.Prompt = "abort"
				m.confirmSourceOperation()
			}
			if result := apply(); result.Err != nil || s.Notice != "" || s.Snapshot.Rebasing || s.Snapshot.Branch != "main" {
				t.Fatalf("%s failed: err=%v notice=%q branch=%q", action, result.Err, s.Notice, s.Snapshot.Branch)
			}
			if action == "abort" && sourceTestGit(t, r, "rev-parse", "HEAD") != before {
				t.Fatal("abort did not restore the original branch")
			}
			if action == "rebase-continue" && sourceTestGit(t, r, "rev-parse", "HEAD^") != incoming {
				t.Fatal("continue did not finish rebasing the current branch")
			}
			if action == "rebase-continue-empty" && sourceTestGit(t, r, "rev-parse", "HEAD") != incoming {
				t.Fatal("continue did not finish a rebase with an empty resolution")
			}
		})
	}
}
