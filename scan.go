package main

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/pelletier/go-toml/v2"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
	"go.abhg.dev/goldmark/wikilink"
)

type Config struct {
	Roots   []string `toml:"roots"`
	Exclude []string `toml:"exclude"`
}

type Orphan struct {
	Path            string  `json:"path"`
	SuggestedParent *string `json:"suggested_parent"`
}

func loadConfig(filename string, required bool) (Config, error) {
	var config Config
	f, err := os.Open(filename)
	if errors.Is(err, os.ErrNotExist) && !required {
		return config, nil
	}
	if err != nil {
		return config, fmt.Errorf("read config: %w", err)
	}
	defer f.Close()
	if err := toml.NewDecoder(f).DisallowUnknownFields().Decode(&config); err != nil {
		return config, fmt.Errorf("parse config: %w", err)
	}
	return config, nil
}

func markdownFiles(root string, excludes []string) (map[string]bool, error) {
	for _, pattern := range excludes {
		if !doublestar.ValidatePattern(pattern) {
			return nil, fmt.Errorf("invalid exclude glob %q", pattern)
		}
	}
	cmd := exec.Command("git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list repository files: %w", err)
	}
	files := make(map[string]bool)
	for _, raw := range bytes.Split(out, []byte{0}) {
		name := string(raw)
		suffix := strings.ToLower(path.Ext(name))
		if suffix != ".md" && suffix != ".markdown" {
			continue
		}
		excluded := false
		for _, pattern := range excludes {
			if match, _ := doublestar.Match(pattern, name); match {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(name)))
		if errors.Is(err, os.ErrNotExist) {
			continue
		} // Tracked deletion.
		if err != nil {
			return nil, fmt.Errorf("inspect %s: %w", name, err)
		}
		// Symlinks are not documents and may point outside the checkout.
		if info.Mode().IsRegular() {
			files[name] = true
		}
	}
	return files, nil
}

func scan(root string, config Config, reachable bool) ([]Orphan, error) {
	files, err := markdownFiles(root, config.Exclude)
	if err != nil {
		return nil, err
	}
	roots := make(map[string]bool)
	for _, name := range config.Roots {
		name = path.Clean(strings.ReplaceAll(name, "\\", "/"))
		if path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
			return nil, fmt.Errorf("root must be repository-relative: %q", name)
		}
		roots[name] = true
		if reachable && !files[name] {
			return nil, fmt.Errorf("entry point %q is missing or excluded", name)
		}
	}
	md := goldmark.New(goldmark.WithExtensions(extension.GFM, extension.Footnote, &wikilink.Extender{}))
	edges := make(map[string][]string)
	incoming := make(map[string]bool)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, source := range names {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(source)))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", source, err)
		}
		document := md.Parser().Parse(text.NewReader(content))
		err = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
			if !entering {
				return ast.WalkContinue, nil
			}
			var destination string
			switch node := node.(type) {
			case *ast.Image:
				return ast.WalkSkipChildren, nil
			case *ast.Link:
				destination = string(util.URLEscape(node.Destination, true))
			case *wikilink.Node:
				if node.Embed {
					return ast.WalkSkipChildren, nil
				}
				destination = string(node.Target)
			}
			if target := resolveLink(source, destination, files); target != "" && target != source {
				edges[source] = append(edges[source], target)
				incoming[target] = true
			}
			return ast.WalkContinue, nil
		})
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", source, err)
		}
	}
	linked := incoming
	if reachable {
		linked = make(map[string]bool)
		queue := make([]string, 0, len(roots))
		for root := range roots {
			linked[root] = true
			queue = append(queue, root)
		}
		for i := 0; i < len(queue); i++ {
			for _, target := range edges[queue[i]] {
				if !linked[target] {
					linked[target] = true
					queue = append(queue, target)
				}
			}
		}
	}
	orphans := make([]Orphan, 0)
	for _, name := range names {
		if !linked[name] && !roots[name] {
			orphans = append(orphans, Orphan{Path: name, SuggestedParent: parentIndex(name, files)})
		}
	}
	return orphans, nil
}

func resolveLink(source, destination string, files map[string]bool) string {
	if destination == "" {
		return ""
	}
	parsed, err := url.Parse(strings.TrimSpace(destination))
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.Path == "" {
		return ""
	}
	// URL.Path is already percent-decoded; decode exactly once, after URL parsing.
	target := strings.ReplaceAll(parsed.Path, "\\", "/")
	if strings.HasPrefix(target, "/") {
		target = strings.TrimLeft(target, "/")
	} else {
		target = path.Join(path.Dir(source), target)
	}
	target = path.Clean(target)
	if target == ".." || strings.HasPrefix(target, "../") {
		return ""
	}
	candidates := []string{target}
	ext := strings.ToLower(path.Ext(target))
	if ext != ".md" && ext != ".markdown" {
		candidates = append(candidates, target+".md", target+".markdown")
	}
	candidates = append(candidates, path.Join(target, "README.md"), path.Join(target, "index.md"))
	for _, candidate := range candidates {
		if files[candidate] {
			return candidate
		}
	}
	return ""
}

func parentIndex(name string, files map[string]bool) *string {
	for dir := path.Dir(name); ; dir = path.Dir(dir) {
		for _, base := range []string{"README.md", "index.md"} {
			candidate := path.Join(dir, base)
			if candidate != name && files[candidate] {
				return &candidate
			}
		}
		if dir == "." {
			return nil
		}
	}
}
