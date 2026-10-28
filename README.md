<p align="center"><img src="assets/logo.png" width="260" alt="Three linked documents"></p>

# md-orphans

Find Markdown files nobody links to. Keep documentation discoverable.

```text
$ md-orphans
docs/deployment.md: unlinked; add a reference from docs/README.md
```

Written in Go. Uses [Goldmark](https://github.com/yuin/goldmark) and its
[wiki-link extension](https://github.com/abhinav/goldmark-wikilink) to parse
Markdown into a syntax tree. No regex-based Markdown parsing.

## Install

Requires Git and Go 1.22+:

```sh
go install github.com/denismaciel/md-orphans@v0.1.0
```

The executable lands in `$(go env GOPATH)/bin` unless `GOBIN` is set. Add that
directory to your `PATH`.

## pre-commit

Add to `.pre-commit-config.yaml`:

```yaml
repos:
  - repo: https://github.com/denismaciel/md-orphans
    rev: v0.1.0
    hooks:
      - id: md-orphans
        # args: [--reachable]
```

Then run `pre-commit install` and `pre-commit run md-orphans --all-files`.
The hook scans the whole repository on every commit, including after deletions.
[pre-commit](https://pre-commit.com/#golang) builds the Go executable in an
isolated environment.

## Usage

```sh
md-orphans                              # Scan the current Git repository
md-orphans --reachable                  # Also catch disconnected link cycles
md-orphans --root README.md --root INDEX.md
md-orphans --exclude 'vendor/**' --json /path/to/repo
```

By default, every Markdown file needs a link from another Markdown file.
`README.md` is the default entry point and needs no incoming link. Self-links
do not count. With `--reachable`, every document must be reachable by following
links from an entry point; missing or excluded entry points are errors.

Save repository defaults in `.md-orphans.toml`:

```toml
roots = ["README.md"]
exclude = ["**/AGENTS.md", "vendor/**", "generated/**"]
```

`--root` and `--exclude` add to the configuration; supplying any roots replaces
the implicit `README.md` default. Exclusions use slash-separated globs, with
`**` matching across directories. Excluded files neither require nor provide
links. Use `--config FILE` for another config, or `--no-config` to ignore it.

Exit codes: **0** clean, **1** orphans found, **2** configuration or runtime error.
`--json` emits a sorted array of `{path, suggested_parent}` objects; a parent
without an available index is `null`. `--help` lists all options.

## What counts

- Inline links, used full/collapsed/shortcut reference links, and `[[wiki|links]]`.
- Links in tables, lists, blockquotes, and footnotes.
- Relative and repository-root-relative paths, URL-encoded filenames, fragments,
  extensionless `.md`/`.markdown` targets, and directory `README.md`/`index.md`.
- Tracked and untracked `.md`/`.markdown` files; Git-ignored untracked files,
  deleted files, symlinks, and submodule contents are skipped.

Code blocks, code spans, comments, images, wiki embeds, external URLs, raw HTML
anchors, and unused reference definitions do not provide links. Paths are
case-sensitive. Broken links and missing heading anchors are outside this tool's
scope. The scan reads working-tree contents; stage related documentation changes
together. Submodules can be checked separately.

The suggested parent is the nearest ancestor `README.md` or `index.md`. In
`--reachable` mode, make sure that parent is reachable too.

## Development

```sh
go test -race ./...
go vet ./...
go run . --reachable
```

Issues and pull requests welcome. Keep changes focused and add regression tests
for parser or link-resolution changes. CI runs tests on Linux, macOS, and Windows.

## License

[MIT](LICENSE). Logo generated with OpenAI image generation; its
[prompt](assets/logo-prompt.txt) is included. Logo and source are provided under
the same license.
