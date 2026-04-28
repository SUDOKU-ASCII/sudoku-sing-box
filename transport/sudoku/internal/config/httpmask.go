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
package config

// HTTPMaskConfig groups all HTTP masking / tunnel related settings.
//
// This is a "presentation layer" config that can be serialized to config.json as:
//
//	"httpmask": {
//	  "disable": false,
//	  "mode": "legacy|stream|poll|auto|ws",
//	  "tls": false,
//	  "host": "",
//	  "path_root": "",
//	  "multiplex": "off|auto|on"
//	}
type HTTPMaskConfig struct {
	Disable bool   `json:"disable"`
	Mode    string `json:"mode"`
	TLS     bool   `json:"tls"`
	Host    string `json:"host"`
	// PathRoot optionally prefixes all HTTP mask paths with a first-level segment.
	// Example: "aabbcc" => "/aabbcc/session", "/aabbcc/api/v1/upload", ...
	PathRoot string `json:"path_root"`
	// Multiplex controls how connections are multiplexed when HTTP mask tunnel modes are enabled:
	//   - "off": disable reuse; each target dials its own HTTPMask tunnel
	//   - "auto": reuse underlying HTTP connections across multiple tunnel dials (HTTP/1.1 keep-alive / HTTP/2)
	//   - "on": keep a single long-lived tunnel and multiplex multiple target streams inside it (single tunnel, multi-target)
	Multiplex string `json:"multiplex"`
}
