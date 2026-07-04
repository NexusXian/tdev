package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}

func findTmux() string {
	for _, c := range []string{"/opt/homebrew/bin/tmux", "/usr/local/bin/tmux", "/usr/bin/tmux"} {
		if info, err := os.Stat(c); err == nil && info.Mode()&0111 != 0 {
			return c
		}
	}
	return "tmux"
}

func arg(i int) string {
	if len(os.Args) > i {
		return os.Args[i]
	}
	return ""
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func capture(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return strings.TrimSpace(string(out)), err
}

func gitOK(target string, args ...string) bool {
	full := append([]string{"-C", target}, args...)
	return exec.Command("git", full...).Run() == nil
}

func gitOut(target string, args ...string) string {
	full := append([]string{"-C", target}, args...)
	out, _ := capture("git", full...)
	return out
}

func main() {
	tmux := findTmux()

	if arg(1) == "close" {
		removeWT, force, branch := parseCloseArgs(os.Args[2:])
		closeWindow(tmux, branch, removeWT, force)
		return
	}

	cur, err := capture(tmux, "display-message", "-p", "#{pane_current_path}")
	if err != nil {
		die("tdev: failed to get current pane path: %v", err)
	}

	branch := arg(1)
	dirArg := arg(2)
	if arg(3) != "" {
		branch = arg(3)
	} else if branch != "" && dirArg != "" && strings.Contains(dirArg, "/") && !isDir(resolveTarget(cur, dirArg)) {
		branch = dirArg
		dirArg = ""
	}

	target := resolveTarget(cur, dirArg)

	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		die("tdev: not a directory: %s", target)
	}

	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	}
	if abs, err := filepath.Abs(target); err == nil {
		target = abs
	}

	var currentBranch string
	if branch != "" {
		if !gitOK(target, "rev-parse", "--is-inside-work-tree") {
			if err := run("git", "-C", target, "init"); err != nil {
				os.Exit(1)
			}
		}
		currentBranch = gitOut(target, "symbolic-ref", "--short", "-q", "HEAD")
		if currentBranch != branch {
			target = ensureWorktree(target, branch)
		}
		currentBranch = branch
	} else {
		currentBranch = gitOut(target, "symbolic-ref", "--short", "-q", "HEAD")
	}

	windowName := currentBranch
	if windowName == "" {
		windowName = "dev"
	}

	left, err := capture(tmux, "new-window", "-P", "-F", "#{pane_id}", "-n", windowName, "-c", target)
	if err != nil {
		die("tdev: failed to create window: %v", err)
	}
	right, err := capture(tmux, "split-window", "-h", "-t", left, "-P", "-F", "#{pane_id}", "-c", target)
	if err != nil {
		die("tdev: failed to split window: %v", err)
	}
	if _, err := capture(tmux, "split-window", "-v", "-t", right, "-l", "20%", "-P", "-F", "#{pane_id}", "-c", target); err != nil {
		die("tdev: failed to split lazygit pane: %v", err)
	}
	run(tmux, "send-keys", "-t", left, "proxy && opencode", "C-m")
	run(tmux, "send-keys", "-t", right, "lazygit", "C-m")
	run(tmux, "select-pane", "-t", left)
}

func parseCloseArgs(args []string) (removeWT, force bool, branch string) {
	for _, a := range args {
		switch a {
		case "-d":
			removeWT = true
		case "-D":
			removeWT, force = true, true
		default:
			if branch == "" {
				branch = a
			}
		}
	}
	return
}

func resolveTarget(cur, dirArg string) string {
	switch {
	case dirArg == "":
		return cur
	case strings.HasPrefix(dirArg, "/"):
		return dirArg
	case strings.HasPrefix(dirArg, "~"):
		return filepath.Join(os.Getenv("HOME"), dirArg[1:])
	default:
		return filepath.Join(cur, dirArg)
	}
}

func closeWindow(tmux, branch string, removeWT, force bool) {
	if removeWT {
		removeWorktree(tmux, branch, force)
	}

	if branch == "" {
		if err := run(tmux, "kill-window"); err != nil {
			die("tdev: failed to close current window: %v", err)
		}
		return
	}

	out, err := capture(tmux, "list-windows", "-F", "#{window_id} #{window_name}")
	if err != nil {
		die("tdev: failed to list windows: %v", err)
	}

	matched := false
	for _, line := range strings.Split(out, "\n") {
		id, wname, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		if wname == branch {
			matched = true
			run(tmux, "kill-window", "-t", id)
		}
	}
	if !matched {
		die("tdev: no window found for worktree: %s", branch)
	}
}

// removeWorktree resolves the worktree for branch (or the current pane's
// branch when branch is empty), removes it, and deletes the branch.
func removeWorktree(tmux, branch string, force bool) {
	cur, err := capture(tmux, "display-message", "-p", "#{pane_current_path}")
	if err != nil || cur == "" {
		die("tdev: failed to get current pane path: %v", err)
	}
	if !gitOK(cur, "rev-parse", "--is-inside-work-tree") {
		die("tdev: not inside a git repository: %s", cur)
	}

	if branch == "" {
		branch = gitOut(cur, "symbolic-ref", "--short", "-q", "HEAD")
		if branch == "" {
			die("tdev: cannot determine branch of current window")
		}
	}

	wt := worktreeForBranch(cur, branch)
	if wt == "" {
		return
	}

	root := mainWorktreeRoot(cur)
	if root == "" {
		die("tdev: failed to locate main worktree")
	}
	if wt == root {
		die("tdev: refusing to remove the main worktree: %s", wt)
	}

	rmArgs := []string{"-C", root, "worktree", "remove", wt}
	if force {
		rmArgs = []string{"-C", root, "worktree", "remove", "--force", wt}
	}
	if err := run("git", rmArgs...); err != nil {
		die("tdev: failed to remove worktree %s (use -D to force)", wt)
	}

	delFlag := "-d"
	if force {
		delFlag = "-D"
	}
	if err := run("git", "-C", root, "branch", delFlag, branch); err != nil {
		fmt.Fprintf(os.Stderr, "tdev: worktree removed but branch %s not deleted (use -D to force)\n", branch)
	}
}

// mainWorktreeRoot returns the path of the primary (first) worktree.
func mainWorktreeRoot(target string) string {
	out := gitOut(target, "worktree", "list", "--porcelain")
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			return strings.TrimPrefix(line, "worktree ")
		}
	}
	return ""
}

func isDir(p string) bool {
	if p == "" {
		return false
	}
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// ensureWorktree returns the path of a git worktree for branch, creating it if
// needed under <repo>-worktrees/<sanitized-branch> next to the repo root.
func ensureWorktree(target, branch string) string {
	root := gitOut(target, "rev-parse", "--show-toplevel")
	if root == "" {
		root = target
	}

	// If target already is the worktree for branch, reuse it.
	if gitOut(target, "symbolic-ref", "--short", "-q", "HEAD") == branch {
		return target
	}

	// Reuse an existing worktree already checked out on branch.
	if wt := worktreeForBranch(target, branch); wt != "" {
		return wt
	}

	dir := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-worktrees", sanitizeBranch(branch))
	if isDir(filepath.Join(dir, ".git")) || isFile(filepath.Join(dir, ".git")) {
		return dir
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		die("tdev: failed to create worktree parent dir: %v", err)
	}

	var args []string
	if gitOK(target, "show-ref", "--verify", "--quiet", "refs/heads/"+branch) {
		args = []string{"-C", root, "worktree", "add", dir, branch}
	} else {
		args = []string{"-C", root, "worktree", "add", "-b", branch, dir}
	}
	if err := run("git", args...); err != nil {
		die("tdev: failed to create worktree for branch %s", branch)
	}
	return dir
}

func worktreeForBranch(target, branch string) string {
	out := gitOut(target, "worktree", "list", "--porcelain")
	if out == "" {
		return ""
	}
	ref := "refs/heads/" + branch
	var path string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			if strings.TrimPrefix(line, "branch ") == ref && isDir(path) {
				return path
			}
		}
	}
	return ""
}

func sanitizeBranch(branch string) string {
	return strings.ReplaceAll(branch, "/", "-")
}

func isFile(p string) bool {
	if p == "" {
		return false
	}
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
