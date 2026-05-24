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
package reverse

import (
	"bytes"
	"testing"
)

func TestRewriteRootAbsolutePaths_Quoted(t *testing.T) {
	in := []byte(`<a href="/x"></a><img src='//cdn/a.png'><a href="/p/y"></a>`)
	got := rewriteRootAbsolutePaths(in, "/p")
	want := []byte(`<a href="/p/x"></a><img src='//cdn/a.png'><a href="/p/y"></a>`)
	if !bytes.Equal(got, want) {
		t.Fatalf("mismatch:\n got: %q\nwant: %q", string(got), string(want))
	}
}

func TestRewriteRootAbsolutePaths_BareSlashValue(t *testing.T) {
	in := []byte(`{"sep":"/","x":"/a"}`)
	got := rewriteRootAbsolutePaths(in, "/p")
	want := []byte(`{"sep":"/","x":"/p/a"}`)
	if !bytes.Equal(got, want) {
		t.Fatalf("mismatch:\n got: %q\nwant: %q", string(got), string(want))
	}
}

func TestRewriteRootAbsolutePaths_EscapedQuotes(t *testing.T) {
	in := []byte(`{\"sep\":\"/\",\"x\":\"/a\"}`)
	got := rewriteRootAbsolutePaths(in, "/p")
	want := in // do not touch escaped-string payload
	if !bytes.Equal(got, want) {
		t.Fatalf("mismatch:\n got: %q\nwant: %q", string(got), string(want))
	}
}

func TestRewriteRootAbsolutePaths_CSSURL(t *testing.T) {
	in := []byte(`body{background:url(/a.png)}a{mask:url( /b.svg )}`)
	got := rewriteRootAbsolutePaths(in, "/p")
	want := []byte(`body{background:url(/p/a.png)}a{mask:url( /p/b.svg )}`)
	if !bytes.Equal(got, want) {
		t.Fatalf("mismatch:\n got: %q\nwant: %q", string(got), string(want))
	}
}

func TestRewriteHTMLSrcset(t *testing.T) {
	in := []byte(`<img srcset="/a.png 1x, /b.png 2x, //cdn/c.png 3x, /p/d.png 4x">`)
	got := rewriteHTMLSrcset(in, "/p")
	want := []byte(`<img srcset="/p/a.png 1x, /p/b.png 2x, //cdn/c.png 3x, /p/d.png 4x">`)
	if !bytes.Equal(got, want) {
		t.Fatalf("mismatch:\n got: %q\nwant: %q", string(got), string(want))
	}
}
