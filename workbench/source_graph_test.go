package workbench

import "testing"

func TestSourceControlGraphJoinsAtParentCommit(t *testing.T) {
	commits := []sourceCommit{
		{Hash: "merge", Parents: "main side"},
		{Hash: "main", Parents: "base"},
		{Hash: "side", Parents: "side-parent"},
		{Hash: "side-parent", Parents: "base"},
		{Hash: "other", Parents: "root"},
		{Hash: "base", Parents: "root"},
		{Hash: "root"},
	}
	var graph sourceGraph
	for i := range commits {
		commit := &commits[i]
		graph.append(commit)
		// Every outgoing path must reappear at the same column and color on
		// the next row, including branches waiting for a shared parent.
		if i > 0 {
			incoming := make(map[int]int)
			if commit.Incoming {
				incoming[commit.Lane] = commit.Color
			}
			for _, edge := range commit.Edges {
				if edge.From != commit.Lane {
					incoming[edge.From] = edge.Color
				}
			}
			for _, join := range commit.Joins {
				incoming[join.From] = join.Color
				if join.To != commit.Lane {
					t.Fatal("join missed the target commit dot")
				}
			}
			previous := commits[i-1]
			if len(incoming) != len(previous.Edges) {
				t.Fatalf("paths disconnected before %s", commit.Hash)
			}
			for _, edge := range previous.Edges {
				if color, ok := incoming[edge.To]; !ok || color != edge.ToColor {
					t.Fatalf("path disconnected or changed color before %s: %+v", commit.Hash, edge)
				}
			}
		}
		if i < 5 && len(commit.Joins) != 0 {
			t.Fatalf("branch merged before reaching its parent: %s", commit.Hash)
		}
	}
	base, root := commits[5], commits[6]
	if len(base.Joins) != 1 || base.Joins[0].Color != commits[2].Color {
		t.Fatal("side branch did not stay separate until the base commit")
	}
	if len(root.Joins) != 1 || root.Joins[0].Color != commits[4].Color {
		t.Fatal("independent branch did not join at the root commit")
	}
}

func TestSourceControlExpandedGraphOffsets(t *testing.T) {
	s := pencodeSourceControl{SelectedHash: "commit", ExpandedCommit: 31, CommitFiles: make([]sourceFile, 20)}
	height := s.graphDetailsHeight()
	for index := range 100 {
		offset := s.graphOffset(index)
		if s.graphCommitAt(offset) != index || s.graphCommitAt(offset+sourceGraphRowHeight-1) != index {
			t.Fatalf("commit %d is unreachable at its expanded offset %d", index, offset)
		}
	}
	start := (s.ExpandedCommit + 1) * sourceGraphRowHeight
	for offset := start; offset < start+height; offset++ {
		if s.graphCommitAt(offset) != s.ExpandedCommit {
			t.Fatal("scrolling inside the file list hid its commit")
		}
	}
	s.GraphScrollY = -float32(s.graphOffset(70))
	s.SelectedHash = ""
	s.resizeGraphDetails(height)
	if s.GraphScrollY != -70*sourceGraphRowHeight || s.graphOffset(70) != 70*sourceGraphRowHeight {
		t.Fatal("collapsing a commit above the viewport moved the visible history")
	}
}

func TestSourceControlCollapseIgnoresPendingFiles(t *testing.T) {
	m := &workspaceModel{}
	s := m.sourceState()
	s.SelectedHash = "0123456789abcdef"
	s.Done = make(chan sourceResult, 1)
	m.sourceAction("sc-commit:" + s.SelectedHash)
	if s.SelectedHash != "" {
		t.Fatal("a pending read prevented collapsing the commit")
	}
	s.Done <- sourceResult{Hash: "0123456789abcdef", Files: []sourceFile{{Path: "resources/main.go"}}}
	if !m.pollSourceResult(s, nil) || s.SelectedHash != "" || len(s.CommitFiles) != 0 {
		t.Fatal("a stale read reopened the collapsed file list")
	}
}
