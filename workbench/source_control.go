package workbench

import (
	"bytes"
	"context"
	"errors"
	"github.com/pencode-labs/pencode/client"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/diiyw/onix/scene"
)

const (
	sourceTextLimit       = 2 << 20
	sourceHistoryPageSize = 32
	sourceGraphRowHeight  = 32
)

type sourceRepository struct {
	Backend *client.Client
	Root    string
	Context context.Context
}
type sourceFile struct {
	Path, Previous string
	Index, Work    byte
	Conflict       bool
}
type sourceCommit struct {
	Hash, Parents, Subject, Author, Date, Refs string
	Lane, Color                                int
	Incoming                                   bool
	Edges                                      []sourceEdge
	Joins                                      []sourceEdge
}
type sourceEdge struct{ From, To, Color, ToColor int }
type sourceSnapshot struct {
	Repository  sourceRepository
	Branch      string
	Branches    []sourceBranch
	Files       []sourceFile
	Groups      [3][]sourceFile // conflicts, index, working tree
	HasHead     bool
	HistoryRefs string
	Sync        sourceSyncStatus
	sourceHistoryPage
}
type sourceDiffKind uint8

const (
	sourceUnchanged sourceDiffKind = iota
	sourceDeleted
	sourceAdded
)

func (kind sourceDiffKind) class() string {
	switch kind {
	case sourceDeleted:
		return "deleted"
	case sourceAdded:
		return "added"
	default:
		return ""
	}
}

type sourceDiffRow struct {
	Left, Right             string
	LeftNumber, RightNumber int
	LeftKind, RightKind     sourceDiffKind
	Changes                 []sourceInlineChange
}
type sourceDiffView struct {
	Rows                     []sourceDiffRow
	CodeColumns, FirstChange int
	Blocks                   []sourceDiffBlock
	Layouts                  [2]scene.TextLineLayout2D
	changes                  []sourceInlineChange
}
type sourceDiffBlock struct {
	FirstRow, EndRow                         int
	LeftStart, LeftEnd, RightStart, RightEnd int
}
type sourceTab struct {
	Key, Path, Revision, Kind      string
	Rows                           []sourceDiffRow
	Local, Incoming, Result, Saved string
	Binary                         bool
	FirstRow                       int
	ScrollX, ScrollY               float32
	CodeColumns                    int
	Highlight                      [3]sourceHighlight
	scrollApplied                  [2][2]float32
	Blocks                         []sourceDiffBlock
	LineLayouts                    [2]scene.TextLineLayout2D
	DiffContent                    string
	DiffRevision                   uint64
	diffChanges                    []sourceInlineChange
	// Disk is the file read when opening; saving refuses to overwrite external edits.
	Disk string
	pencodeEditHistory
}

func (t *sourceTab) editable() bool { return t.Kind == "changes" && !t.Binary }

func (r sourceRepository) baseContext() context.Context {
	if r.Context != nil {
		return r.Context
	}
	return context.Background()
}

func (r sourceRepository) command(args ...string) ([]byte, error) {
	return r.Backend.Git(r.baseContext(), r.Root, args...)
}

func discoverSourceRepository(backend *client.Client, directory string) (sourceRepository, error) {
	r := sourceRepository{Root: directory, Backend: backend}
	data, err := r.command("rev-parse", "--show-toplevel")
	if err != nil {
		return r, err
	}
	r.Root = strings.TrimSuffix(string(data), "\n")
	return r, nil
}

func parseSourceStatus(data []byte) ([]sourceFile, error) {
	var files []sourceFile
	for len(data) > 0 {
		record, rest, ok := bytes.Cut(data, []byte{0})
		if !ok || len(record) < 4 || record[2] != ' ' {
			return nil, errors.New("invalid Git status record")
		}
		data = rest
		f := sourceFile{Path: string(record[3:]), Index: record[0], Work: record[1]}
		f.Conflict = f.Index == 'U' || f.Work == 'U' || f.Index == 'A' && f.Work == 'A' || f.Index == 'D' && f.Work == 'D'
		if f.Index == 'R' || f.Index == 'C' || f.Work == 'R' || f.Work == 'C' {
			previous, next, valid := bytes.Cut(data, []byte{0})
			if !valid {
				return nil, errors.New("invalid Git rename record")
			}
			f.Previous, data = string(previous), next
		}
		files = append(files, f)
	}
	slices.SortFunc(files, func(a, b sourceFile) int { return strings.Compare(a.Path, b.Path) })
	return files, nil
}

func (r sourceRepository) statusSnapshot() (sourceSnapshot, error) {
	s := sourceSnapshot{Repository: r}
	data, err := r.command("status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return s, err
	}
	if s.Files, err = parseSourceStatus(data); err != nil {
		return s, err
	}
	for _, f := range s.Files {
		if f.Conflict {
			s.Groups[0] = append(s.Groups[0], f)
			continue
		}
		if f.Index != ' ' && f.Index != '?' {
			s.Groups[1] = append(s.Groups[1], f)
		}
		if f.Work != ' ' || f.Index == '?' {
			s.Groups[2] = append(s.Groups[2], f)
		}
	}
	return s, nil
}

func (r sourceRepository) snapshot(limit int) (sourceSnapshot, error) {
	s, err := r.statusSnapshot()
	if err != nil {
		return s, err
	}
	branch, err := r.command("symbolic-ref", "--quiet", "--short", "HEAD")
	if err == nil {
		s.Branch = strings.TrimSpace(string(branch))
	}
	if s.Branches, err = r.branches(); err != nil {
		return s, err
	}
	head, headErr := r.command("rev-parse", "--verify", "HEAD")
	s.HasHead = headErr == nil
	if s.Branch == "" && s.HasHead {
		s.Branch = strings.TrimSpace(string(head))[:8] + " (detached)"
	}
	if !s.HasHead {
		return s, nil
	}
	refs, err := r.command("show-ref", "--head")
	if err != nil {
		return s, err
	}
	s.HistoryRefs = string(refs)
	// An unconfigured upstream has no synchronization counts.
	s.Sync, _ = r.syncStatus()
	s.sourceHistoryPage, err = r.historyPage(s.HistoryRefs, 0, limit, sourceGraph{})
	return s, err
}

type sourceSyncStatus struct{ Ahead, Behind int }

func (s sourceSnapshot) syncPending() bool {
	return len(s.Files) == 0 && (s.Sync.Ahead != 0 || s.Sync.Behind != 0)
}

func (r sourceRepository) syncStatus() (sourceSyncStatus, error) {
	var status sourceSyncStatus
	data, err := r.command("rev-list", "--left-right", "--count", "HEAD...@{upstream}")
	if err != nil {
		return status, err
	}
	left, right, ok := strings.Cut(strings.TrimSpace(string(data)), "\t")
	if !ok {
		return status, errors.New("invalid Git synchronization counts")
	}
	if status.Ahead, err = strconv.Atoi(left); err != nil {
		return status, err
	}
	status.Behind, err = strconv.Atoi(right)
	return status, err
}

func (r sourceRepository) syncChanges() error {
	data, err := r.command("status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return err
	}
	if len(data) != 0 {
		return errors.New("请先提交或清理工作区修改，再同步远程。")
	}
	if _, err := r.command("fetch"); err != nil {
		return err
	}
	status, err := r.syncStatus()
	if err != nil {
		return err
	}
	if status.Behind != 0 {
		if _, err := r.command("pull", "--no-rebase", "--no-edit"); err != nil {
			return err
		}
	}
	if status.Ahead != 0 {
		_, err = r.command("push")
	}
	return err
}

type sourceHistoryPage struct {
	Commits []sourceCommit
	Graph   sourceGraph
	HasMore bool
}

// Pin the traversal to the captured ref tips. A branch change while loading a
// later page cannot shift offsets or duplicate commits in the current graph.
func (r sourceRepository) historyPage(refs string, offset, limit int, previous sourceGraph) (sourceHistoryPage, error) {
	page := sourceHistoryPage{Graph: sourceGraph{lanes: slices.Clone(previous.lanes), nextColor: previous.nextColor}}
	args := []string{"log", "--date-order", "--date=short", "--format=%H%x00%P%x00%s%x00%an%x00%ad%x00%D%x00", "--skip=" + strconv.Itoa(offset), "-n", strconv.Itoa(limit + 1)}
	for line := range strings.SplitSeq(refs, "\n") {
		if hash, _, ok := strings.Cut(line, " "); ok {
			args = append(args, hash)
		}
	}
	args = append(args, "--")
	data, err := r.command(args...)
	if err != nil {
		return page, err
	}
	for len(data) > 0 {
		data = bytes.TrimLeft(data, "\n")
		if len(data) == 0 {
			break
		}
		if len(page.Commits) == limit {
			page.HasMore = true
			break
		}
		var fields [6]string
		for i := range fields {
			value, rest, ok := bytes.Cut(data, []byte{0})
			if !ok {
				return page, errors.New("invalid Git log record")
			}
			fields[i], data = string(value), rest
		}
		commit := sourceCommit{Hash: fields[0], Parents: fields[1], Subject: fields[2], Author: fields[3], Date: fields[4], Refs: fields[5]}
		page.Graph.append(&commit)
		page.Commits = append(page.Commits, commit)
	}
	return page, nil
}

type sourceGraphLane struct {
	Hash  string
	Color int
}

type sourceGraph struct {
	lanes, next []sourceGraphLane
	nextColor   int
}

func (g *sourceGraph) allocateColor(used uint8) int {
	color := g.nextColor
	for range sourceGraphColorCount {
		if used&(1<<color) == 0 {
			break
		}
		color = (color + 1) % sourceGraphColorCount
	}
	g.nextColor = (color + 1) % sourceGraphColorCount
	return color
}

func (g *sourceGraph) append(c *sourceCommit) {
	lanes := g.lanes
	next := g.next[:0]
	lane := -1
	var usedColors uint8
	// Keep each branch pending until its parent commit is actually visited.
	// Several lanes may target that same parent; they join at its dot.
	for i, branch := range lanes {
		usedColors |= 1 << branch.Color
		if branch.Hash == c.Hash {
			if lane < 0 {
				lane = i
			} else {
				c.Joins = append(c.Joins, sourceEdge{From: i, To: lane, Color: branch.Color, ToColor: lanes[lane].Color})
			}
		} else {
			next = append(next, branch)
		}
	}
	c.Incoming = lane >= 0
	if lane < 0 {
		lane = len(lanes)
		lanes = append(lanes, sourceGraphLane{Hash: c.Hash, Color: g.allocateColor(usedColors)})
		usedColors |= 1 << lanes[lane].Color
	}
	c.Lane, c.Color = lane, lanes[lane].Color
	survivors := len(next)
	parents := strings.Fields(c.Parents)
	for p, parent := range parents {
		position := len(next)
		color := c.Color
		if p == 0 {
			position = lane
		} else {
			color = g.allocateColor(usedColors)
			usedColors |= 1 << color
		}
		next = slices.Insert(next, position, sourceGraphLane{Hash: parent, Color: color})
	}
	survivor := 0
	for from, branch := range lanes {
		if from == lane {
			for p := range parents {
				to := lane
				if p != 0 {
					to = survivors + p
				}
				color := next[to].Color
				c.Edges = append(c.Edges, sourceEdge{From: from, To: to, Color: color, ToColor: next[to].Color})
			}
		} else if branch.Hash != c.Hash {
			to := survivor
			if len(parents) != 0 && to >= lane {
				to++
			}
			c.Edges = append(c.Edges, sourceEdge{From: from, To: to, Color: branch.Color, ToColor: next[to].Color})
			survivor++
		}
	}
	g.lanes, g.next = next, lanes[:0]
}

func (r sourceRepository) commitFiles(hash string) ([]sourceFile, error) {
	data, err := r.command("diff-tree", "--root", "--no-commit-id", "--name-status", "-r", "-z", "-M", hash+"^!", "--")
	// Explicitly compare merges to their first parent, matching the history diff.
	if parents, e := r.command("rev-list", "--parents", "-n", "1", hash); e == nil && len(strings.Fields(string(parents))) > 1 {
		data, err = r.command("diff", "--name-status", "-z", "-M", hash+"^", hash, "--")
	}
	if err != nil {
		return nil, err
	}
	var files []sourceFile
	for len(data) > 0 {
		status, rest, ok := bytes.Cut(data, []byte{0})
		if !ok || len(status) == 0 {
			return nil, errors.New("invalid Git commit file record")
		}
		name, rest, ok := bytes.Cut(rest, []byte{0})
		if !ok {
			return nil, errors.New("invalid Git commit path")
		}
		f := sourceFile{Path: string(name), Index: status[0]}
		if f.Index == 'R' || f.Index == 'C' {
			name, rest, ok = bytes.Cut(rest, []byte{0})
			if !ok {
				return nil, errors.New("invalid Git commit rename")
			}
			f.Previous, f.Path = f.Path, string(name)
		}
		files, data = append(files, f), rest
	}
	slices.SortFunc(files, func(a, b sourceFile) int { return strings.Compare(a.Path, b.Path) })
	return files, nil
}

func (r sourceRepository) validatePath(path string) (string, error) {
	if path == "" || strings.ContainsRune(path, 0) || filepath.IsAbs(path) {
		return "", errors.New("invalid repository path")
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("path is outside the repository")
	}
	return filepath.Join(r.Root, clean), nil
}

func (r sourceRepository) workFile(path string) ([]byte, error) {
	if _, err := r.validatePath(path); err != nil {
		return nil, err
	}
	data, err := r.Backend.WorkFile(r.baseContext(), r.Root, path)
	if len(data) > sourceTextLimit {
		return nil, errors.New("file exceeds the 2 MiB text comparison limit")
	}
	return data, err
}

func (r sourceRepository) objectFile(revision, path string) ([]byte, error) {
	if _, err := r.validatePath(path); err != nil {
		return nil, err
	}
	object := revision + ":" + path
	size, err := r.command("cat-file", "-s", object)
	if err != nil {
		// An absent side is expected for added/deleted files and add/delete conflicts.
		return nil, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(size)))
	if err != nil || n > sourceTextLimit {
		return nil, errors.New("file exceeds the 2 MiB text comparison limit")
	}
	return r.command("cat-file", "blob", object)
}

func sourceHasMarkers(value string) bool {
	for line := range strings.SplitSeq(value, "\n") {
		for _, marker := range []string{"<<<<<<<", "|||||||", "=======", ">>>>>>>"} {
			if strings.HasPrefix(line, marker) && (len(line) == len(marker) || line[len(marker)] == ' ' || line[len(marker)] == '\r') {
				return true
			}
		}
	}
	return false
}

func (r sourceRepository) stage(files []sourceFile) error {
	// Validate the complete batch before touching the index.
	args := []string{"add", "--"}
	for _, f := range files {
		if _, err := r.validatePath(f.Path); err != nil {
			return err
		}
		args = append(args, f.Path)
		if f.Previous != "" && (f.Work == 'R' || f.Work == 'C') {
			if _, err := r.validatePath(f.Previous); err != nil {
				return err
			}
			args = append(args, f.Previous)
		}
	}
	if len(files) == 0 {
		return nil
	}
	unmerged, err := r.command(append([]string{"ls-files", "--unmerged", "-z", "--"}, args[2:]...)...)
	if err != nil {
		return err
	}
	conflicts := make(map[string]bool)
	for record := range bytes.SplitSeq(unmerged, []byte{0}) {
		if _, name, ok := bytes.Cut(record, []byte{'\t'}); ok {
			conflicts[string(name)] = true
		}
	}
	for _, f := range files {
		if f.Conflict || conflicts[f.Path] {
			data, err := r.workFile(f.Path)
			if err != nil {
				return err
			}
			if sourceHasMarkers(string(data)) {
				return errors.New("请先解决合并冲突，再暂存并提交。")
			}
		}
	}
	if len(args) == 2 {
		return nil
	}
	_, err = r.command(args...)
	return err
}

func (r sourceRepository) unstage(files []sourceFile, hasHead bool) error {
	args := []string{"restore", "--staged", "--"}
	if !hasHead {
		args = []string{"rm", "--cached", "--ignore-unmatch", "--"}
	}
	for _, f := range files {
		if _, err := r.validatePath(f.Path); err != nil {
			return err
		}
		args = append(args, f.Path)
		if f.Previous != "" {
			args = append(args, f.Previous)
		}
	}
	if len(files) == 0 {
		return nil
	}
	_, err := r.command(args...)
	return err
}

func (r sourceRepository) discardChanges(files []sourceFile) error {
	for _, f := range files {
		_, err := r.validatePath(f.Path)
		if err != nil {
			return err
		}
		if f.Index == '?' || f.Work == '?' {
			workspace, err := r.Backend.OpenWorkspace(r.baseContext(), r.Root)
			if err == nil {
				err = r.Backend.Remove(r.baseContext(), workspace.ID, f.Path)
			}
			if err != nil {
				return err
			}
			continue
		}
		if _, err := r.command("restore", "--worktree", "--", f.Path); err != nil {
			return err
		}
	}
	return nil
}

func (r sourceRepository) commit(message string) error {
	if strings.TrimSpace(message) == "" {
		return errors.New("请输入提交说明。")
	}
	status, err := r.command("status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return err
	}
	files, err := parseSourceStatus(status)
	if err != nil {
		return err
	}
	staged := false
	for _, file := range files {
		if file.Conflict {
			return errors.New("请先解决并暂存所有合并冲突，再提交。")
		}
		staged = staged || file.Index != ' ' && file.Index != '?'
	}
	if !staged {
		if err := r.stage(files); err != nil {
			return err
		}
	}
	_, err = r.command("commit", "-m", message)
	return err
}

func (r sourceRepository) openDiff(file sourceFile, kind, revision string) (*sourceTab, error) {
	t := &sourceTab{Key: kind + ":" + revision + ":" + file.Path, Path: file.Path, Kind: kind, Revision: revision}
	var left, right []byte
	var err error
	oldPath := file.Path
	if file.Previous != "" {
		oldPath = file.Previous
	}
	switch kind {
	case "merge":
		left, err = r.objectFile(":2", file.Path)
		if err == nil {
			right, err = r.objectFile(":3", file.Path)
		}
		if err == nil {
			var result []byte
			result, err = r.workFile(file.Path)
			t.Result, t.Saved, t.Disk = string(result), string(result), string(result)
		}
		t.Local, t.Incoming = string(left), string(right)
	case "history":
		if file.Index != 'A' {
			left, err = r.objectFile(revision+"^", oldPath)
		}
		if err == nil && file.Index != 'D' {
			right, err = r.objectFile(revision, file.Path)
		}
	case "staged":
		if file.Index != 'A' {
			left, err = r.objectFile("HEAD", oldPath)
		}
		if err == nil && file.Index != 'D' {
			right, err = r.objectFile("", file.Path)
		}
	default:
		if file.Index != '?' {
			left, err = r.objectFile("", file.Path)
		}
		if err == nil {
			right, err = r.workFile(file.Path)
		}
	}
	if err != nil {
		return nil, err
	}
	t.Binary = bytes.IndexByte(left, 0) >= 0 || bytes.IndexByte(right, 0) >= 0 || !utf8.Valid(left) || !utf8.Valid(right) || !utf8.ValidString(t.Result)
	if !t.Binary {
		t.Local, t.Incoming = string(left), string(right)
		if t.editable() {
			t.Result, t.Saved, t.Disk = t.Incoming, t.Incoming, t.Incoming
		}
		var diff sourceDiffView
		diff, err = sourceDiffContext(r.baseContext(), r.Backend, t.Local, t.Incoming)
		t.initializeDiffView(diff)
	}
	return t, err
}

// Git's bounded-memory diff algorithm supplies edit spans. Unchanged lines are
// included in the aligned result, with no quadratic Go LCS matrix.
func sourceDiff(backend *client.Client, left, right []byte) ([]sourceDiffRow, error) {
	view, err := sourceDiffContext(context.Background(), backend, string(left), string(right))
	return view.Rows, err
}

func sourceDiffContext(parent context.Context, backend *client.Client, left, right string) (sourceDiffView, error) {
	patch, err := backend.Diff(parent, left, right)
	if err != nil {
		return sourceDiffView{}, err
	}
	return alignSourceDiff(left, right, string(patch)), nil
}

type sourceLine struct {
	Text       string
	Start, End int
}

func sourceLines(value string) []sourceLine {
	lines := make([]sourceLine, 0, strings.Count(value, "\n")+1)
	offset := 0
	for text := range strings.SplitAfterSeq(value, "\n") {
		if text != "" {
			lines = append(lines, sourceLine{strings.TrimSuffix(text, "\n"), offset, offset + len(text)})
		}
		offset += len(text)
	}
	return lines
}

func alignSourceDiff(left, right, patch string) sourceDiffView {
	var view sourceDiffView
	var inline sourceInlineDiffer
	alignSourceDiffInto(&view, &inline, left, right, patch)
	return view
}

// The caller exclusively owns the retained buffers until it publishes the view.
func alignSourceDiffInto(view *sourceDiffView, inline *sourceInlineDiffer, left, right, patch string) {
	// Stream line slices directly into rows; no document-sized sourceLine arrays.
	counts := [2]int{}
	for side, value := range [...]string{left, right} {
		counts[side] = strings.Count(value, "\n")
		if value != "" && !strings.HasSuffix(value, "\n") {
			counts[side]++
		}
	}
	clear(view.Rows)
	clear(view.changes)
	view.Rows = slices.Grow(view.Rows[:0], max(counts[0], counts[1]))
	view.Blocks = view.Blocks[:0]
	view.CodeColumns, view.FirstChange = 0, -1
	inline.changes = view.changes[:0]
	for side, count := range counts {
		view.Layouts[side].Offsets = slices.Grow(view.Layouts[side].Offsets[:0], count+1)
	}
	l, r, lb, rb := 0, 0, 0, 0
	nextLine := func(value string, offset *int) string {
		start := *offset
		end := len(value)
		if n := strings.IndexByte(value[start:], '\n'); n >= 0 {
			end = start + n
			*offset = end + 1
		} else {
			*offset = end
		}
		return value[start:end]
	}
	appendRows := func(lc, rc int, changed bool) {
		block := sourceDiffBlock{FirstRow: len(view.Rows), LeftStart: lb, RightStart: rb}
		for i := 0; i < max(lc, rc); i++ {
			row := sourceDiffRow{}
			if i < lc && l < counts[0] {
				row.Left, row.LeftNumber = nextLine(left, &lb), l+1
				view.Layouts[0].Offsets = append(view.Layouts[0].Offsets, float32(len(view.Rows)*20))
				l++
				if changed {
					row.LeftKind = sourceDeleted
				}
			}
			if i < rc && r < counts[1] {
				row.Right, row.RightNumber = nextLine(right, &rb), r+1
				view.Layouts[1].Offsets = append(view.Layouts[1].Offsets, float32(len(view.Rows)*20))
				r++
				if changed {
					row.RightKind = sourceAdded
				}
			}
			if changed && view.FirstChange < 0 {
				view.FirstChange = len(view.Rows)
			}
			if changed {
				row.Changes = inline.compare(row.Left, row.Right)
			}
			columns := sourceCodeColumns(row.Left)
			if row.Right != row.Left {
				columns = max(columns, sourceCodeColumns(row.Right))
			}
			view.CodeColumns = max(view.CodeColumns, columns)
			view.Rows = append(view.Rows, row)
		}
		if changed && len(view.Rows) > block.FirstRow {
			block.EndRow, block.LeftEnd, block.RightEnd = len(view.Rows), lb, rb
			view.Blocks = append(view.Blocks, block)
		}
	}
	for line := range strings.SplitSeq(patch, "\n") {
		if !strings.HasPrefix(line, "@@ -") {
			continue
		}
		leftRange, rest, found := strings.Cut(line[3:], " ")
		rightRange, _, endRange := strings.Cut(rest, " ")
		if !found || !endRange {
			continue
		}
		parseRange := func(value string) (int, int) {
			a, b, has := strings.Cut(value[1:], ",")
			start, _ := strconv.Atoi(a)
			count := 1
			if has {
				count, _ = strconv.Atoi(b)
			}
			if count > 0 {
				start--
			}
			return start, count
		}
		ls, lc := parseRange(leftRange)
		rs, rc := parseRange(rightRange)
		appendRows(max(0, ls-l), max(0, rs-r), false)
		appendRows(lc, rc, true)
	}
	appendRows(counts[0]-l, counts[1]-r, false)
	for side, text := range [...]string{left, right} {
		layout := &view.Layouts[side]
		if text == "" || strings.HasSuffix(text, "\n") {
			layout.Offsets = append(layout.Offsets, float32(len(view.Rows)*20))
		}
		layout.Width, layout.Height = float32(view.CodeColumns*8+10), float32((len(view.Rows)+1)*20)
	}
	view.changes = inline.changes
}

func (r sourceRepository) saveMerge(t sourceTab) error {
	if sourceHasMarkers(t.Result) {
		return errors.New("请先移除所有冲突标记，再完成合并。")
	}
	if err := r.saveWorktree(t); err != nil {
		return err
	}
	_, err := r.command("add", "--", t.Path)
	return err
}

func (r sourceRepository) saveWorktree(t sourceTab) error {
	if _, err := r.validatePath(t.Path); err != nil {
		return err
	}
	return r.Backend.SaveFile(r.baseContext(), r.Root, t.Path, t.Disk, t.Result)
}
