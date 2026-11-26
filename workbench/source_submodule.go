package workbench

import (
	"slices"
	"strings"
)

func missingSourceSubmodule(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	const marker = "'git status --porcelain=2' failed in submodule "
	i := strings.LastIndex(message, marker)
	if i < 0 || !strings.Contains(message[:i], "cannot chdir to ") || !strings.Contains(message[:i], "No such file or directory") {
		return ""
	}
	return message[i+len(marker):]
}

// Limit each override to this status read so a restored worktree is picked up
// on the next refresh. Commit and sync still require a complete status.
func (r sourceRepository) snapshotStatus() ([]byte, error) {
	args := []string{"status", "--porcelain=v1", "-z", "--untracked-files=all"}
	for {
		data, err := r.command(args...)
		path := missingSourceSubmodule(err)
		if path == "" {
			return data, err
		}
		key, lookupErr := r.command("config", "--file", ".gitmodules", "--name-only", "--fixed-value", "--get-regexp", "--", `^submodule\..*\.path$`, path)
		name, ok := strings.CutSuffix(strings.TrimSuffix(string(key), "\n"), ".path")
		override := name + ".ignore=dirty"
		if lookupErr != nil || !ok || strings.ContainsAny(name, "=\n") || slices.Contains(args, override) {
			// The gitlink can outlive its .gitmodules entry during a checkout.
			return r.command(append(args, "--ignore-submodules=dirty")...)
		}
		args = slices.Insert(args, 0, "-c", override)
	}
}

// Inspect modes before reading content: a gitlink points to a commit, not a blob.
// Raw output also detects deleted submodules and file/submodule type changes.
func (r sourceRepository) submoduleSummary(file sourceFile, kind, revision string) (string, error) {
	if _, err := r.validatePath(file.Path); err != nil {
		return "", err
	}
	args := []string{"diff", "--raw", "--no-ext-diff", "--no-textconv", "--no-renames", "--ignore-submodules=none", "--submodule=short"}
	switch kind {
	case "history":
		args[0] = "diff-tree"
		args = append(args, "--root", "--no-commit-id", "-r", "--first-parent", "-m", revision)
	case "staged":
		args = append(args, "--cached")
	}
	args = append(args, "--", file.Path)
	if file.Previous != "" {
		if _, err := r.validatePath(file.Previous); err != nil {
			return "", err
		}
		args = append(args, file.Previous)
	}
	raw, err := r.command(args...)
	if err != nil {
		return "", err
	}
	for line := range strings.SplitSeq(string(raw), "\n") {
		if !strings.HasPrefix(line, ":") {
			continue
		}
		// Unmerged raw records may have more than one colon before the modes.
		modes := strings.TrimLeft(line, ":")
		submodule := false
		for range len(line) - len(modes) + 1 {
			mode, rest, _ := strings.Cut(modes, " ")
			modes = rest
			if mode == "160000" {
				submodule = true
				break
			}
		}
		if !submodule {
			continue
		}
		args[1] = "--patch"
		patch, err := r.command(args...)
		if err != nil {
			return "", err
		}
		return "Submodule · " + file.Path + "\n\n" + string(patch), nil
	}
	return "", nil
}
