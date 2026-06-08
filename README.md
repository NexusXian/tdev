# tdev

tmux dev workspace launcher.

## Install

```
go install github.com/NexusXian/tdev@latest
```

## Usage

```
tdev [name] [dir] [branch]
```

- `name` window name (default `dev`)
- `dir` target dir: absolute / `~`-relative / relative to current pane (default current pane path)
- `branch` optional git branch to switch/create

Opens a new tmux window split into two panes: left runs `proxy && opencode`, right runs `lazygit`.
