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
// Package rewrite puts a stripped path prefix back into upstream responses, so a web app
// that is mounted under a subpath of the reverse listener keeps working in a browser.
package rewrite

import "net/http"

// Response puts prefix back into what the upstream sent for a route mounted at prefix (which must
// not be the root "/"): redirects (Location: /foo), cookie paths (Path=/) and root-absolute asset
// URLs in HTML/CSS/SVG/JSON ("/assets/...", url(/assets/...)).
func Response(resp *http.Response, prefix string) error {
	rewriteLocation(resp, prefix)
	rewriteSetCookiePath(resp, prefix)
	rewriteReversePrefixCookie(resp, prefix)
	return rewriteTextBody(resp, prefix)
}
