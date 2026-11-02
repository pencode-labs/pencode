package workbench

import "strings"

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
