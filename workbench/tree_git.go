package workbench

import (
	"path/filepath"
	"slices"
	"strings"
)

// Higher priorities win when several descendant states decorate a folder.
type treeGitStatus uint8

const (
	treeGitClean treeGitStatus = iota
	treeGitUntracked
	treeGitAdded
	treeGitRenamed
	treeGitDeleted
	treeGitModified
	treeGitConflict
)

func (status treeGitStatus) nameClass() string {
	switch status {
	case treeGitUntracked:
		return "pencode-tree-name git-untracked"
	case treeGitAdded:
		return "pencode-tree-name git-added"
	case treeGitRenamed:
		return "pencode-tree-name git-renamed"
	case treeGitDeleted:
		return "pencode-tree-name git-deleted"
	case treeGitModified:
		return "pencode-tree-name git-modified"
	case treeGitConflict:
		return "pencode-tree-name git-conflict"
	default:
		return "pencode-tree-name"
	}
}

func treeGitFileStatus(file sourceFile) treeGitStatus {
	if file.Conflict {
		return treeGitConflict
	}
	status := file.Work
	if status == ' ' {
		status = file.Index
	}
	switch status {
	case '?':
		return treeGitUntracked
	case 'A':
		return treeGitAdded
	case 'R', 'C':
		return treeGitRenamed
	case 'D':
		return treeGitDeleted
	case 'M', 'T':
		return treeGitModified
	default:
		return treeGitClean
	}
}

type pencodeTreeGit struct {
	files map[string][]sourceFile
	names map[string]treeGitStatus
}

// Refresh the other repositories too, without changing the source control
// selection. The selected repository's status is already in the job result.
func (r sourceRepository) treeGitFiles(roots []string) map[string][]sourceFile {
	var files map[string][]sourceFile
	for _, root := range roots {
		if root == r.Root {
			continue
		}
		repository := r
		repository.Root = root
		snapshot, err := repository.statusSnapshot()
		if err != nil {
			continue
		}
		if files == nil {
			files = make(map[string][]sourceFile)
		}
		files[root] = snapshot.Files
	}
	return files
}

// Rebuild only on Git results. Rendering uses one map lookup per visible row,
// independent of folding, the number of descendants, and directory scans.
func (git *pencodeTreeGit) update(project string, roots []string, files map[string][]sourceFile, snapshot *sourceSnapshot) {
	if git.files == nil {
		git.files = make(map[string][]sourceFile)
		git.names = make(map[string]treeGitStatus)
	}
	for root := range git.files {
		if !slices.Contains(roots, root) {
			delete(git.files, root)
		}
	}
	for root, changes := range files {
		git.files[root] = changes
	}
	if snapshot != nil && snapshot.Repository.Root != "" {
		git.files[snapshot.Repository.Root] = snapshot.Files
	}
	clear(git.names)
	for root, changes := range git.files {
		for _, file := range changes {
			name := strings.TrimRight(root, "/") + "/" + file.Path
			// Nested repositories own their contents; a parent may report the
			// repository directory itself as modified or untracked.
			if owner := sourceRepositoryForPath(roots, name); owner == root || name == owner {
				git.markPath(project, name, treeGitFileStatus(file))
			}
			if file.Previous != "" && (file.Index == 'R' || file.Work == 'R') {
				git.markPath(project, strings.TrimRight(root, "/")+"/"+file.Previous, treeGitDeleted)
			}
		}
	}
}

func (git *pencodeTreeGit) markPath(project, name string, status treeGitStatus) {
	relative, err := filepath.Rel(project, filepath.FromSlash(name))
	if err == nil && filepath.IsLocal(relative) && relative != "." {
		git.mark(filepath.ToSlash(relative), status)
	}
}

func (git *pencodeTreeGit) mark(name string, status treeGitStatus) {
	for {
		git.names[name] = max(git.names[name], status)
		end := strings.LastIndexByte(name, '/')
		if end < 0 {
			return
		}
		name = name[:end]
	}
}
