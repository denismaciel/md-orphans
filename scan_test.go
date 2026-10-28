package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func git(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
}

func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	git(t, root, "init", "-q")
	for name, content := range files {
		filename := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestMarkdownSemantics(t *testing.T) {
	tests := []struct {
		name, markdown string
		linked         bool
	}{
		{"inline", "[guide](docs/guide.md)", true},
		{"title", "[guide](docs/guide.md \"Title\")", true},
		{"nested label", "[**a [nested] label**](docs/guide.md)", true},
		{"full reference", "[guide][g]\n\n[g]: docs/guide.md", true},
		{"collapsed reference", "[guide][]\n\n[guide]: docs/guide.md", true},
		{"shortcut reference", "[guide]\n\n[guide]: docs/guide.md", true},
		{"unused definition", "[guide]: docs/guide.md", false},
		{"wiki", "[[docs/guide|Read me]]", true},
		{"wiki fragment", "[[docs/guide#Heading]]", true},
		{"table", "| A |\n| --- |\n| [guide](docs/guide.md) |", true},
		{"footnote", "See[^a].\n\n[^a]: [guide](docs/guide.md)", true},
		{"inline code", "`[guide](docs/guide.md)`", false},
		{"fenced code", "```md\n[guide](docs/guide.md)\n```", false},
		{"long fence", "````md\n```\n[guide](docs/guide.md)\n````", false},
		{"indented code", "    [guide](docs/guide.md)", false},
		{"escaped markup", "\\[guide](docs/guide.md)", false},
		{"comment", "<!-- [guide](docs/guide.md) -->", false},
		{"html block", "<pre>\n[guide](docs/guide.md)\n</pre>", false},
		{"image", "![guide](docs/guide.md)", false},
		{"wiki embed", "![[docs/guide]]", false},
		{"external", "[guide](https://example.com/docs/guide.md)", false},
		{"protocol relative", "[guide](//example.com/docs/guide.md)", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := repo(t, map[string]string{"README.md": tc.markdown, "docs/guide.md": ""})
			got, err := scan(root, Config{Roots: []string{"README.md"}}, false)
			if err != nil {
				t.Fatal(err)
			}
			if (len(got) == 0) != tc.linked {
				t.Fatalf("linked=%v, orphans=%v", tc.linked, got)
			}
		})
	}
}

func TestResolveLink(t *testing.T) {
	files := map[string]bool{"docs/guide.md": true, "space name.md": true, "hash#name.md": true, "%20.md": true, "docs/a(b).md": true, "docs/version.2.md": true, "docs/index.md": true, "README.md": true}
	tests := []struct{ source, destination, want string }{
		{"README.md", "docs/guide.md#heading", "docs/guide.md"},
		{"README.md", "docs/guide.md?raw=1#heading", "docs/guide.md"},
		{"docs/guide.md", "../space%20name.md", "space name.md"},
		{"docs/guide.md", "/README.md", "README.md"},
		{"README.md", "docs/guide", "docs/guide.md"},
		{"README.md", "docs/version.2", "docs/version.2.md"},
		{"README.md", "docs/", "docs/index.md"},
		{"README.md", "hash%23name.md", "hash#name.md"},
		{"README.md", "%2520.md", "%20.md"},
		{"README.md", "docs/a(b).md", "docs/a(b).md"},
		{"README.md", "#heading", ""},
		{"README.md", "../README.md", ""},
		{"README.md", "https://example.com/README.md", ""},
		{"README.md", "//example.com/README.md", ""},
		{"README.md", "missing.md", ""},
		{"README.md", "bad%ZZ.md", ""},
	}
	for _, tc := range tests {
		if got := resolveLink(tc.source, tc.destination, files); got != tc.want {
			t.Errorf("resolveLink(%q, %q)=%q; want %q", tc.source, tc.destination, got, tc.want)
		}
	}
}

func TestDestinationEscapes(t *testing.T) {
	root := repo(t, map[string]string{
		"README.md":     "[space](<space name.md>)\n[paren](a\\(b\\).md)\n[entity](a&amp;b.md)",
		"space name.md": "", "a(b).md": "", "a&b.md": "",
	})
	got, err := scan(root, Config{Roots: []string{"README.md"}}, false)
	if err != nil || len(got) != 0 {
		t.Fatalf("orphans=%v, err=%v", got, err)
	}
}

func TestReachabilityAndSelfLinks(t *testing.T) {
	root := repo(t, map[string]string{
		"README.md": "[index](docs/README.md)", "docs/README.md": "[guide](guide.md)", "docs/guide.md": "",
		"a.md": "[b](b.md)", "b.md": "[a](a.md)", "self.md": "[self](self.md)",
	})
	config := Config{Roots: []string{"README.md"}}
	for _, mode := range []bool{false, true} {
		got, err := scan(root, config, mode)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, orphan := range got {
			names = append(names, orphan.Path)
		}
		want := []string{"self.md"}
		if mode {
			want = []string{"a.md", "b.md", "self.md"}
		}
		if !reflect.DeepEqual(names, want) {
			t.Errorf("reachable=%v: got %v; want %v", mode, names, want)
		}
	}
	config.Roots = append(config.Roots, "a.md", "self.md")
	if got, err := scan(root, config, true); err != nil || len(got) != 0 {
		t.Fatalf("multiple roots: %v, %v", got, err)
	}
	config.Roots = []string{"missing.md"}
	if _, err := scan(root, config, true); err == nil {
		t.Fatal("missing entry point must fail in reachable mode")
	}
}

func TestGitInventoryAndSuggestions(t *testing.T) {
	root := repo(t, map[string]string{
		"README.md": "[index](docs/README.md)", "docs/README.md": "",
		"docs/child/orphan.MD": "", "deleted.md": "", "ignored.md": "",
		"vendor/deep/generated.md": "", "AGENTS.md": "", "a/AGENTS.md": "",
		".gitignore": "ignored.md\n",
	})
	git(t, root, "add", ".")
	if err := os.Remove(filepath.Join(root, "deleted.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "new.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := scan(root, Config{Roots: []string{"README.md"}, Exclude: []string{"vendor/**", "**/AGENTS.md"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	wantParent, rootParent := "docs/README.md", "README.md"
	want := []Orphan{{"docs/child/orphan.MD", &wantParent}, {"new.md", &rootParent}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
	if _, err := scan(root, Config{Exclude: []string{"["}}, false); err == nil {
		t.Fatal("invalid glob accepted")
	}
}

func TestExclusionsCannotProvideIncomingLinks(t *testing.T) {
	root := repo(t, map[string]string{"README.md": "", "excluded.md": "[guide](guide.md)", "guide.md": ""})
	got, err := scan(root, Config{Roots: []string{"README.md"}, Exclude: []string{"excluded.md"}}, false)
	if err != nil || len(got) != 1 || got[0].Path != "guide.md" {
		t.Fatalf("%v, %v", got, err)
	}
}

func TestSymlinksAreSkipped(t *testing.T) {
	root := repo(t, map[string]string{"README.md": ""})
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("private"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "symlink.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if got, err := scan(root, Config{Roots: []string{"README.md"}}, false); err != nil || len(got) != 0 {
		t.Fatalf("symlink scanned: %v, %v", got, err)
	}
}

func TestNoParentAndInvalidRoot(t *testing.T) {
	root := repo(t, map[string]string{"alone.md": ""})
	got, err := scan(root, Config{}, false)
	if err != nil || len(got) != 1 || got[0].SuggestedParent != nil {
		t.Fatalf("%v, %v", got, err)
	}
	for _, name := range []string{"../README.md", "/README.md"} {
		if _, err := scan(root, Config{Roots: []string{name}}, false); err == nil {
			t.Errorf("accepted invalid root %q", name)
		}
	}
}

func TestConfigAndCLI(t *testing.T) {
	root := repo(t, map[string]string{"README.md": "", "INDEX.md": "", "draft.md": "", ".md-orphans.toml": "roots = ['README.md', 'INDEX.md']\nexclude = ['draft.md']\n"})
	tests := []struct {
		args     []string
		code     int
		contains string
	}{
		{[]string{root}, 0, "No unlinked"},
		{[]string{root, "--json"}, 0, "[]"},
		{[]string{"--no-config", root, "--json"}, 1, "INDEX.md"},
		{[]string{root, "--no-config", "--root", "README.md", "--root", "INDEX.md", "--exclude", "draft.md"}, 0, "No unlinked"},
		{[]string{root, "--config", "missing.toml"}, 2, "read config"},
		{[]string{root, "--config", "x", "--no-config"}, 2, "mutually exclusive"},
		{[]string{root, "extra"}, 2, "at most one"},
		{[]string{"--root"}, 2, "needs a value"},
		{[]string{"--unknown"}, 2, "not defined"},
		{[]string{"--help"}, 0, "Usage:"},
		{[]string{"--version"}, 0, "md-orphans"},
		{[]string{t.TempDir()}, 2, "cannot locate Git repository"},
	}
	for _, tc := range tests {
		var stdout, stderr bytes.Buffer
		got := run(tc.args, &stdout, &stderr)
		if got != tc.code || !strings.Contains(stdout.String()+stderr.String(), tc.contains) {
			t.Errorf("args=%v: code %d, out=%q, err=%q", tc.args, got, stdout.String(), stderr.String())
		}
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{root, "--no-config", "--json"}, &stdout, &stderr); code != 1 {
		t.Fatal(code)
	}
	var results []Orphan
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil || len(results) != 2 {
		t.Fatalf("invalid JSON: %s, %v", &stdout, err)
	}
	for _, content := range []string{"unexpected = true", "roots = 'README.md'", "exclude = [1]", "["} {
		if err := os.WriteFile(filepath.Join(root, ".md-orphans.toml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := loadConfig(filepath.Join(root, ".md-orphans.toml"), true); err == nil {
			t.Errorf("accepted invalid config %q", content)
		}
	}
}
