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
package httpmask

import (
	"io"
	"net/http"
	"testing"
)

func BenchmarkApplyTunnelHeaders(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		h := make(http.Header, 8)
		applyTunnelHeaders(h, "example.com", TunnelModeStream)
	}
}

func BenchmarkUploadURL(b *testing.B) {
	const push = "http://golden.test:80/r/api/v1/upload?token=T-k_9"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = uploadURL(push, uint64(i)+1)
	}
}

func BenchmarkWriteRandomRequestHeader(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := WriteRandomRequestHeaderWithPathRoot(io.Discard, "example.com:443", "aabbcc"); err != nil {
			b.Fatal(err)
		}
	}
}
