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
	"testing"
	"time"
)

func TestPipeDeadline_ReusesTimerAcrossSets(t *testing.T) {
	d := makePipeDeadline()
	expired := func() bool { return isClosedPipeChan(d.wait()) }

	d.set(time.Now().Add(time.Hour))
	d.set(time.Time{})
	d.set(time.Now().Add(20 * time.Millisecond))
	if expired() {
		t.Fatal("deadline fired before it was due")
	}
	select {
	case <-d.wait():
	case <-time.After(2 * time.Second):
		t.Fatal("re-armed deadline never fired")
	}

	// Extending an expired deadline must hand out a fresh, open channel.
	d.set(time.Now().Add(time.Hour))
	if expired() {
		t.Fatal("extended deadline is still expired")
	}
	d.set(time.Now().Add(-time.Second))
	if !expired() {
		t.Fatal("past deadline did not expire immediately")
	}
	d.set(time.Time{})
	if expired() {
		t.Fatal("cleared deadline is still expired")
	}
}
