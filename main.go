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

	name := arg(1)
	if name == "" {
		name = "dev"
	}

	cur, err := capture(tmux, "display-message", "-p", "#{pane_current_path}")
	if err != nil {
		die("tdev: failed to get current pane path: %v", err)
	}

	dirArg := arg(2)
	branch := arg(3)

	var target string
	switch {
	case dirArg == "":
		target = cur
	case strings.HasPrefix(dirArg, "/"):
		target = dirArg
	case strings.HasPrefix(dirArg, "~"):
		target = filepath.Join(os.Getenv("HOME"), dirArg[1:])
	default:
		target = filepath.Join(cur, dirArg)
	}

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
			rebaseMerge := gitOut(target, "rev-parse", "--git-path", "rebase-merge")
			rebaseApply := gitOut(target, "rev-parse", "--git-path", "rebase-apply")
			if isDir(rebaseMerge) || isDir(rebaseApply) {
				fmt.Fprintf(os.Stderr, "tdev: cannot switch branch while rebase is in progress: %s\n", target)
				fmt.Fprintf(os.Stderr, "tdev: run git -C \"%s\" rebase --continue or git -C \"%s\" rebase --abort\n", target, target)
				os.Exit(1)
			}
			if gitOK(target, "show-ref", "--verify", "--quiet", "refs/heads/"+branch) {
				if err := run("git", "-C", target, "switch", branch); err != nil {
					os.Exit(1)
				}
			} else {
				if err := run("git", "-C", target, "switch", "-c", branch); err != nil {
					os.Exit(1)
				}
			}
		}
		currentBranch = branch
	} else {
		currentBranch = gitOut(target, "symbolic-ref", "--short", "-q", "HEAD")
	}

	windowName := name
	if currentBranch != "" {
		windowName = fmt.Sprintf("%s[%s]", name, currentBranch)
	}

	left, err := capture(tmux, "new-window", "-P", "-F", "#{pane_id}", "-n", windowName, "-c", target)
	if err != nil {
		die("tdev: failed to create window: %v", err)
	}
	right, err := capture(tmux, "split-window", "-h", "-t", left, "-P", "-F", "#{pane_id}", "-c", target)
	if err != nil {
		die("tdev: failed to split window: %v", err)
	}
	run(tmux, "send-keys", "-t", left, "proxy && opencode", "C-m")
	run(tmux, "send-keys", "-t", right, "lazygit", "C-m")
	run(tmux, "select-pane", "-t", left)
}

func isDir(p string) bool {
	if p == "" {
		return false
	}
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
