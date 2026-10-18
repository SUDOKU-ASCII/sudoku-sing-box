/*
Copyright (C) 2026 by saba <contact me via issue>

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program. If not, see <http://www.gnu.org/licenses/>.

In addition, no derivative work may use the name or imply association
with this application without prior consent.
*/
package rewrite

import "testing"

func TestRewriteHTMLRootAbsolutePaths(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"no script", `<a href="/x"></a>`, `<a href="/p/x"></a>`},
		{"inline script body is skipped",
			`<a href="/x"></a><script>var u="/api";</script><a href="/y"></a>`,
			`<a href="/p/x"></a><script>var u="/api";</script><a href="/p/y"></a>`},
		{"script attributes are rewritten",
			`<script src="/static/a.js"></script>`,
			`<script src="/p/static/a.js"></script>`},
		{"quoted > inside the script tag",
			`<script data-x="a>b" src="/s.js">var a="/x";</script>`,
			`<script data-x="a>b" src="/p/s.js">var a="/x";</script>`},
		{"upper-case tags",
			`<SCRIPT>var a="/x";</SCRIPT><a href="/y">`,
			`<SCRIPT>var a="/x";</SCRIPT><a href="/p/y">`},
		{"several scripts",
			`"/a"<script>"/b"</script>"/c"<script type="module">"/d"</script>"/e"`,
			`"/p/a"<script>"/b"</script>"/p/c"<script type="module">"/d"</script>"/p/e"`},
		{"script-like tag name is not a script",
			`<scripts>"/x"</scripts>`,
			`<scripts>"/p/x"</scripts>`},
		{"unterminated script falls back to rewriting everything",
			`<script>var a="/x";`,
			`<script>var a="/p/x";`},
		{"unterminated tag falls back to rewriting everything",
			`<script src="/x"`,
			`<script src="/p/x"`},
		{"nothing to rewrite", `<script>var a="/x";</script><p>hi</p>`, `<script>var a="/x";</script><p>hi</p>`},
		{"already prefixed", `<a href="/p/x"></a>`, `<a href="/p/x"></a>`},
		{"empty", ``, ``},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(rewriteHTMLRootAbsolutePaths([]byte(tc.in), "/p")); got != tc.want {
				t.Fatalf("\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}

	for _, prefix := range []string{"", "/", "  "} {
		in := `<a href="/x"></a>`
		if got := string(rewriteHTMLRootAbsolutePaths([]byte(in), prefix)); got != in {
			t.Errorf("prefix %q must be a no-op, got %q", prefix, got)
		}
	}
}

func TestRewriteRootAbsolutePathsTable(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"single quotes", `'/a'`, `'/p/a'`},
		{"backticks", "`/a`", "`/p/a`"},
		{"protocol relative", `"//cdn/a"`, `"//cdn/a"`},
		{"bare slash value", `"/"`, `"/"`},
		{"slash before closing quote or paren", `("/") '/' ` + "`/`", `("/") '/' ` + "`/`"},
		{"relative", `"a/b"`, `"a/b"`},
		{"already prefixed exact", `"/p"`, `"/p"`},
		{"already prefixed with query", `"/p?x=1" "/p#f" "/p/a"`, `"/p?x=1" "/p#f" "/p/a"`},
		{"string-prefix sibling is not prefixed", `"/pp/a"`, `"/p/pp/a"`},
		{"escaped quote start is skipped", `\"/a\"`, `\"/a\"`},
		{"css url unquoted", `url(/a.png)`, `url(/p/a.png)`},
		{"css url spaced and upper-case", `URL( /a.png )`, `URL( /p/a.png )`},
		{"css url quoted", `url("/a.png")`, `url("/p/a.png")`},
		{"css url protocol relative", `url(//cdn/a.png)`, `url(//cdn/a.png)`},
		{"css url prefixed", `url(/p/a.png)`, `url(/p/a.png)`},
		{"css url relative", `url(a.png)`, `url(a.png)`},
		{"mixed", `{"a":"/x","b":"y","c":"//z"}`, `{"a":"/p/x","b":"y","c":"//z"}`},
		{"json inside a string value", `{"html":"<a href=\"/x\">"}`, `{"html":"<a href=\"/x\">"}`},
		{"trailing quote only", `"`, `"`},
		{"empty", ``, ``},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(rewriteRootAbsolutePaths([]byte(tc.in), "/p")); got != tc.want {
				t.Fatalf("\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

func TestRewriteHTMLSrcsetTable(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"candidates", `<img srcset="/a.png 1x, /b.png 2x">`, `<img srcset="/p/a.png 1x, /p/b.png 2x">`},
		{"single candidate", `<img srcset="/a.png 1x">`, `<img srcset="/p/a.png 1x">`},
		{"only the second candidate needs a prefix", `<img srcset="/p/a.png 1x, /b.png 2x">`, `<img srcset="/p/a.png 1x, /p/b.png 2x">`},
		{"protocol relative and prefixed untouched", `<img srcset="//c/a.png 1x,/p/b.png 2x">`, `<img srcset="//c/a.png 1x,/p/b.png 2x">`},
		{"relative untouched", `<img srcset="a.png 1x, b.png 2x">`, `<img srcset="a.png 1x, b.png 2x">`},
		{"attribute name is case-insensitive", `<img SRCSET = '/a.png 1x, /b.png 2x'>`, `<img SRCSET = '/p/a.png 1x, /p/b.png 2x'>`},
		{"unquoted value is skipped", `<img srcset=/a.png>`, `<img srcset=/a.png>`},
		{"unterminated value is skipped", `<img srcset="/a.png 1x`, `<img srcset="/a.png 1x`},
		{"no srcset", `<a href="/a">`, `<a href="/a">`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(rewriteHTMLSrcset([]byte(tc.in), "/p")); got != tc.want {
				t.Fatalf("\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
	in := `<img srcset="/a.png 1x">`
	if got := string(rewriteHTMLSrcset([]byte(in), "")); got != in {
		t.Errorf("empty prefix must be a no-op, got %q", got)
	}
}

func TestRewriteTextPayload(t *testing.T) {
	tests := []struct {
		name, contentType, in, want string
	}{
		{"html srcset, single candidate", "text/html", `<img srcset="/a.png 1x">`, `<img srcset="/p/a.png 1x">`},
		{"html srcset, several candidates", "text/html", `<img srcset="/a.png 1x, /b.png 2x">`, `<img srcset="/p/a.png 1x, /p/b.png 2x">`},
		{"html script skipped, srcset rewritten", "text/html", `<script>"/x"</script><img srcset="/a.png 1x, /b.png 2x">`, `<script>"/x"</script><img srcset="/p/a.png 1x, /p/b.png 2x">`},
		{"xhtml", "application/xhtml+xml", `<script>"/x"</script>"/y"`, `<script>"/x"</script>"/p/y"`},
		{"json has no script handling", "application/json", `"/a"`, `"/p/a"`},
		{"css", "text/css", `a{b:url(/i.png)}`, `a{b:url(/p/i.png)}`},
		{"nil stays nil", "text/html", ``, ``},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var in []byte
			if tc.in != "" {
				in = []byte(tc.in)
			}
			got := rewriteTextPayload(tc.contentType, in, "/p")
			if string(got) != tc.want {
				t.Fatalf("\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

// These pin behaviour that looks wrong but has shipped; change them only on purpose.
func TestRewriteKnownQuirks(t *testing.T) {
	t.Run("closing quote followed by /> is read as the start of a URL", func(t *testing.T) {
		in := `<image href="/a.png"/>`
		want := `<image href="/p/a.png"/p/>`
		if got := string(rewriteRootAbsolutePaths([]byte(in), "/p")); got != want {
			t.Fatalf("\n got: %q\nwant: %q", got, want)
		}
	})

}
